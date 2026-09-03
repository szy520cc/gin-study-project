package test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"myproject/internal/config"
	"myproject/internal/resource"
	"myproject/internal/router"
	"myproject/pkg/errcode"
	"myproject/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// engineWith 用调整过的配置另起一个引擎，用于验证限流、体积上限这类配置驱动的行为。
// resource 已由 TestMain 注入，所以这里只需要 config。
func engineWith(t *testing.T, tune func(*config.Config)) http.Handler {
	t.Helper()

	cfg := *resource.Cfg() // 值拷贝，不污染全局
	cfg.Server.Mode = "test"
	if tune != nil {
		tune(&cfg)
	}
	engine, err := router.Setup(&cfg)
	require.NoError(t, err)
	return engine
}

// TestNotFoundAndMethodNotAllowed 404/405 都要是 JSON，不能是 gin 默认的纯文本
func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	code, resp := do(t, http.MethodGet, "/not-exist", "", nil)
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, errcode.ErrNotFound.Code(), resp.Code)

	// /livez 只注册了 GET；别拿 /api/v1/users/login 试，
	// 它的 GET 会先命中 /users/:id 那条通配路由，走不到 NoMethod
	code, resp = do(t, http.MethodPost, "/livez", "", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, code)
	assert.Equal(t, errcode.ErrMethodNotAllowed.Code(), resp.Code)
}

// TestAuthGuard 受保护路由缺少/伪造 token 一律 401
func TestAuthGuard(t *testing.T) {
	cases := map[string]string{
		"没有 Authorization 头": "",
		"格式不对":               "Basic abc",
		"token 是伪造的":         "Bearer not-a-jwt",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/users/profile", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			w := httptest.NewRecorder()
			testEngine.ServeHTTP(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}

// TestValidationMessage 校验错误要翻成中文、字段名用 json tag，且不暴露内部结构体名
func TestValidationMessage(t *testing.T) {
	// 校验在绑参阶段就失败，不会走到数据库
	code, resp := do(t, http.MethodPost, "/api/v1/users/register", "", map[string]string{
		"username": "alice",
		"password": "secret123",
		"email":    "not-an-email",
	})

	require.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, resp.Details, "email")
	assert.Contains(t, resp.Details, "邮箱")
	assert.NotContains(t, resp.Details, "UserRegisterRequest", "不应暴露内部结构体名")
}

// TestBindQueryErrorHidesInternals query 绑定失败不能把 strconv 的错误串吐给调用方
func TestBindQueryErrorHidesInternals(t *testing.T) {
	// 绑参失败发生在查库之前，用现签的 token 即可，不需要真实用户
	token := testToken(t)

	code, resp := do(t, http.MethodGet, "/api/v1/orders?page=abc", token, nil)
	require.Equal(t, http.StatusBadRequest, code)
	assert.NotContains(t, resp.Details, "strconv")
	assert.NotContains(t, resp.Details, "ParseInt")
}

// TestBodyLimit 请求体超限直接 413，不读取一个字节
func TestBodyLimit(t *testing.T) {
	srv := engineWith(t, func(cfg *config.Config) { cfg.Server.MaxBodyBytes = 64 })

	payload := `{"username":"alice","password":"secret123","remark":"` + strings.Repeat("x", 512) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// TestProbesBypassRateLimit 探针必须绕开全局限流。
// 探针和业务流量共用令牌桶时，过载瞬间 kubelet 会拿到 429：
// liveness 判失败就重启容器，恰好在最需要实例的时候把实例杀掉。
func TestProbesBypassRateLimit(t *testing.T) {
	srv := engineWith(t, func(cfg *config.Config) {
		cfg.RateLimit.Enabled = true
		cfg.RateLimit.RPS = 1
		cfg.RateLimit.Burst = 1
		cfg.RateLimit.AuthRPS = 1
		cfg.RateLimit.AuthBurst = 1
	})

	for _, path := range []string{"/livez", "/readyz", "/health"} {
		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			require.NotEqual(t, http.StatusTooManyRequests, w.Code, "%s 第 %d 次被限流", path, i+1)
		}
	}

	// 业务路由仍受限流约束，否则这个用例等于什么都没验证
	var limited bool
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/users/9", nil))
		if w.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	assert.True(t, limited, "业务路由应仍然受全局限流约束")
}

// TestPanicCountedAs500 panic 请求必须以真实状态码进指标。
// Recovery 若注册在 Metrics 外层，defer 里读到的是 gin 的默认状态 200，
// 最需要告警的那类请求会被记成成功。
func TestPanicCountedAs500(t *testing.T) {
	const route = "/boom"

	cfg := *resource.Cfg()
	cfg.Server.Mode = "test"
	engine, err := router.Setup(&cfg)
	require.NoError(t, err)
	engine.GET(route, func(*gin.Context) { panic("boom") })

	before500 := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "500"))
	before200 := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "200"))

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, before500+1,
		testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "500")),
		"panic 请求应计入 status=500")
	assert.Equal(t, before200,
		testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "200")),
		"panic 请求不应被记成 200")
}

// TestMetricsRouteLabelIsTemplate route 标签必须是路由模板，否则每个 ID 一条时间序列。
// 用未认证请求即可：中间件在 controller 之前就返回 401，但 FullPath 已经解析出模板。
func TestMetricsRouteLabelIsTemplate(t *testing.T) {
	const route = "/api/v1/users/:id"
	before := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "401"))

	w := httptest.NewRecorder()
	testEngine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/users/12345", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)

	after := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "401"))
	assert.Equal(t, before+1, after, "指标应记在路由模板上，而不是真实路径 /api/v1/users/12345")

	// 真实路径不应产生自己的时间序列
	assert.Zero(t, testutil.ToFloat64(
		metrics.RequestsTotal.WithLabelValues(http.MethodGet, "/api/v1/users/12345", "401")))
}

// TestSecurityHeaders 安全响应头必须存在
func TestSecurityHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	testEngine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.NotEmpty(t, w.Header().Get("X-Request-Id"))
}

// testToken 现签一个 token，用于不需要真实用户的框架层用例
func testToken(t *testing.T) string {
	t.Helper()
	token, err := resource.JWT().GenerateToken(7, "framework-test")
	require.NoError(t, err)
	return token
}
