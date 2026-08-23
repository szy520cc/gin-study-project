package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"myproject/internal/config"
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/module"
	"myproject/internal/router"
	"myproject/pkg/auth"
	"myproject/pkg/errcode"
	"myproject/pkg/health"
	"myproject/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newServerWithConfig 允许用例自定义配置，用于验证限流、体积上限这类配置驱动的行为
func newServerWithConfig(t *testing.T, tune func(*config.Config)) http.Handler {
	t.Helper()

	cfg := &config.Config{}
	cfg.Server.Mode = "test"
	cfg.Server.RequestTimeout = 5
	cfg.Server.MaxBodyBytes = 1 << 20
	cfg.CORS.AllowOrigins = []string{"*"}
	if tune != nil {
		tune(cfg)
	}

	jwtManager := auth.NewJWTManager(strings.Repeat("k", 32), time.Hour, "myproject")

	engine, err := router.Setup(cfg, health.NewRegistry(time.Second, 0),
		module.Deps{JWT: jwtManager}, stubModules(&stubUserService{}))
	require.NoError(t, err)
	return engine
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

// TestHTTP_MethodNotAllowed 验证 405 真的会被触发。
// gin 的 HandleMethodNotAllowed 默认关闭，不显式打开时 NoMethod 注册的
// handler 永远不会被调用，方法不匹配会被当成 404。
func TestHTTP_MethodNotAllowed(t *testing.T) {
	srv := newServerWithConfig(t, nil)

	w := httptest.NewRecorder()
	// /livez 只注册了 GET。注意别拿 /api/v1/users/login 试：
	// 它的 GET 会先命中 /users/:id 这条通配路由，走不到 NoMethod。
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/livez", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Equal(t, float64(errcode.ErrMethodNotAllowed.Code()), decodeBody(t, w)["code"])
}

// TestHTTP_BodyLimit 验证请求体上限：Content-Length 声明超限时直接拒绝，
// 不读取一个字节。
func TestHTTP_BodyLimit(t *testing.T) {
	srv := newServerWithConfig(t, func(cfg *config.Config) {
		cfg.Server.MaxBodyBytes = 64
	})

	payload := fmt.Sprintf(`{"username":"alice","password":"secret123","remark":%q}`, strings.Repeat("x", 512))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Equal(t, float64(errcode.ErrBodyTooLarge.Code()), decodeBody(t, w)["code"])
}

// TestHTTP_ValidationMessage 验证校验错误被翻成中文，且字段名用 json tag。
// 原来是把 go-playground 的英文原串直接吐出去，既没法用，
// 还暴露了内部结构体名（UserRegisterRequest.Email）。
func TestHTTP_ValidationMessage(t *testing.T) {
	srv := newServerWithConfig(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/register",
		strings.NewReader(`{"username":"alice","password":"secret123","email":"not-an-email"}`))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	details, _ := decodeBody(t, w)["details"].(string)
	assert.Contains(t, details, "email")
	assert.Contains(t, details, "邮箱")
	assert.NotContains(t, details, "UserRegisterRequest", "不应暴露内部结构体名")
}

// TestHTTP_AuthRateLimit 验证注册/登录有独立的更严配额。
// bcrypt 每次校验是 CPU 密集操作，用普通接口的配额挡不住暴力破解。
func TestHTTP_AuthRateLimit(t *testing.T) {
	srv := newServerWithConfig(t, func(cfg *config.Config) {
		cfg.RateLimit.Enabled = true
		cfg.RateLimit.RPS = 1000
		cfg.RateLimit.Burst = 1000
		cfg.RateLimit.AuthRPS = 1
		cfg.RateLimit.AuthBurst = 2
	})

	post := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/login",
			strings.NewReader(`{"username":"alice","password":"secret123"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code
	}

	var limited bool
	for i := 0; i < 5; i++ {
		if post() == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	assert.True(t, limited, "登录接口应在 auth_burst 用尽后返回 429")

	// 普通接口配额宽松，不应被认证配额牵连
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestErrCode_FromContextErrors 验证超时/取消不会被伪装成 500。
// ErrTimeout 此前定义了却没有任何地方产生它：超时最终被兜成「内部错误」，
// 日志刷 error、告警误报，排查方向被带偏。
func TestErrCode_FromContextErrors(t *testing.T) {
	t.Run("超时映射为 504", func(t *testing.T) {
		err := errcode.From(fmt.Errorf("query users: %w", context.DeadlineExceeded))

		assert.Equal(t, errcode.ErrTimeout.Code(), err.Code())
		assert.Equal(t, http.StatusGatewayTimeout, err.HTTPStatus())
	})

	t.Run("客户端取消单独归类", func(t *testing.T) {
		err := errcode.From(fmt.Errorf("query users: %w", context.Canceled))

		assert.Equal(t, errcode.ErrClientClosed.Code(), err.Code())
		assert.Equal(t, errcode.StatusClientClosedRequest, err.HTTPStatus())
	})

	t.Run("其余仍归为内部错误", func(t *testing.T) {
		err := errcode.From(fmt.Errorf("boom"))

		assert.Equal(t, errcode.ErrInternal.Code(), err.Code())
	})
}

// TestHealthRegistry_Cache 验证探测结果缓存生效：/readyz 无认证且会真打下游，
// 不缓存的话外部可以拿它当放大器持续压 DB/Redis。
func TestHealthRegistry_Cache(t *testing.T) {
	var calls int
	reg := health.NewRegistry(time.Second, time.Minute)
	reg.RegisterFunc("fake", func(context.Context) error {
		calls++
		return nil
	})

	for i := 0; i < 3; i++ {
		require.True(t, reg.Check(context.Background()).Healthy)
	}

	assert.Equal(t, 1, calls, "缓存期内不应重复探测下游")
}

// TestHealthRegistry_CheckerPanic 验证探测实现 panic 时报告为不健康而不是杀进程
func TestHealthRegistry_CheckerPanic(t *testing.T) {
	reg := health.NewRegistry(time.Second, 0)
	reg.RegisterFunc("boom", func(context.Context) error { panic("checker exploded") })

	report := reg.Check(context.Background())

	assert.False(t, report.Healthy)
	assert.Equal(t, "error", report.Services["boom"].Status)
}

// TestFormatCents 验证金额格式化全程整数运算，不引入浮点误差
func TestFormatCents(t *testing.T) {
	cases := map[int64]string{
		0:      "0.00",
		5:      "0.05",
		99:     "0.99",
		100:    "1.00",
		1999:   "19.99",
		100000: "1000.00",
		-1050:  "-10.50",
	}
	for cents, want := range cases {
		assert.Equal(t, want, model.FormatCents(cents), "cents=%d", cents)
	}
}

// TestHTTP_ProbesBypassRateLimit 探针必须绕开全局限流。
// 探针和业务流量共用令牌桶时，过载瞬间 kubelet 会拿到 429：
// liveness 判失败就重启容器，恰好在最需要实例的时候把实例杀掉。
func TestHTTP_ProbesBypassRateLimit(t *testing.T) {
	srv := newServerWithConfig(t, func(cfg *config.Config) {
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

// TestHTTP_PanicCountedAs500 panic 请求必须以真实状态码进指标，并留下访问日志。
// Recovery 若注册在 Metrics 外层，defer 里读到的是 gin 的默认状态 200，
// 最需要告警的那类请求会被记成成功。
func TestHTTP_PanicCountedAs500(t *testing.T) {
	const route = "/api/v1/boom"

	boom := func(g *gin.RouterGroup, d module.Deps) {
		g.GET("/boom", func(*gin.Context) { panic("boom") })
	}
	cfg := &config.Config{}
	cfg.Server.Mode = "test"
	cfg.Server.RequestTimeout = 5
	cfg.Server.MaxBodyBytes = 1 << 20
	cfg.CORS.AllowOrigins = []string{"*"}
	jwtManager := auth.NewJWTManager(strings.Repeat("k", 32), time.Hour, "myproject")
	srv, err := router.Setup(cfg, health.NewRegistry(time.Second, 0),
		module.Deps{JWT: jwtManager}, []module.Register{boom})
	require.NoError(t, err)

	before500 := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "500"))
	before200 := testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "200"))

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, before500+1,
		testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "500")),
		"panic 请求应计入 status=500")
	assert.Equal(t, before200,
		testutil.ToFloat64(metrics.RequestsTotal.WithLabelValues(http.MethodGet, route, "200")),
		"panic 请求不应被记成 200")
}

// TestHTTP_BindQueryErrorHidesInternals query 绑定失败不能把标准库错误串吐给调用方。
// gin 的 form 映射会把 strconv.ParseInt 的原始错误上抛。
func TestHTTP_BindQueryErrorHidesInternals(t *testing.T) {
	srv, jwtManager := newTestServer(t, &stubUserService{})
	token, err := jwtManager.GenerateToken(7, "alice")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders?page=abc", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	details, _ := decodeBody(t, w)["details"].(string)
	assert.NotContains(t, details, "strconv", "不应暴露标准库函数名")
	assert.NotContains(t, details, "ParseInt")
}

// TestHTTP_MissingAuthIsRejected 路由漏挂 Auth 时必须 401，
// 不能拿 user_id=0 继续读写数据。
func TestHTTP_MissingAuthIsRejected(t *testing.T) {
	// 刻意不给 Deps.Auth 挂中间件，模拟新增模块时漏挂认证
	noAuth := func(g *gin.RouterGroup, d module.Deps) {
		module.OrderWith(g, module.Deps{Auth: middleware.NoOp(), AuthLimit: middleware.NoOp()}, &stubOrderService{})
	}
	cfg := &config.Config{}
	cfg.Server.Mode = "test"
	cfg.Server.RequestTimeout = 5
	cfg.Server.MaxBodyBytes = 1 << 20
	cfg.CORS.AllowOrigins = []string{"*"}
	jwtManager := auth.NewJWTManager(strings.Repeat("k", 32), time.Hour, "myproject")
	srv, err := router.Setup(cfg, health.NewRegistry(time.Second, 0),
		module.Deps{JWT: jwtManager}, []module.Register{noAuth})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestHealthRegistry_OverallTimeout 不理 ctx 的 checker 不能让探测永久挂住：
// 那会导致缓存永远写不进去，每个新探针请求都再起一批 goroutine。
func TestHealthRegistry_OverallTimeout(t *testing.T) {
	reg := health.NewRegistry(100*time.Millisecond, time.Minute)
	reg.RegisterFunc("stuck", func(context.Context) error {
		time.Sleep(10 * time.Second) // 完全无视 ctx
		return nil
	})

	done := make(chan health.Report, 1)
	go func() { done <- reg.Check(context.Background()) }()

	select {
	case report := <-done:
		assert.False(t, report.Healthy)
		assert.Equal(t, "error", report.Services["stuck"].Status)
	case <-time.After(3 * time.Second):
		t.Fatal("探测没有在整体超时内返回")
	}
}
