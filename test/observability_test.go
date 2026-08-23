package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"myproject/internal/config"
	"myproject/pkg/admin"
	"myproject/pkg/health"
	"myproject/pkg/metrics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeLabels 从注册表里取出某个指标的所有 route 标签值
func routeLabels(t *testing.T, metricName string) map[string]bool {
	t.Helper()

	families, err := metrics.Registry.Gather()
	require.NoError(t, err)

	labels := map[string]bool{}
	for _, mf := range families {
		if mf.GetName() != metricName {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "route" {
					labels[l.GetValue()] = true
				}
			}
		}
	}
	return labels
}

// TestMetrics_RouteLabelIsTemplate 验证 route 标签用的是路由模板而不是真实路径。
//
// 这是指标接入里最容易埋的坑：用真实路径的话，每个用户 ID 都会产生一条
// 独立时间序列，基数无上限增长，先撑爆 Prometheus 再撑爆自己的内存。
func TestMetrics_RouteLabelIsTemplate(t *testing.T) {
	srv, jwtManager := newTestServer(t, &stubUserService{})
	token, err := jwtManager.GenerateToken(7, "alice")
	require.NoError(t, err)

	for _, id := range []string{"7", "8", "9"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+id, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		srv.ServeHTTP(httptest.NewRecorder(), req)
	}

	labels := routeLabels(t, "http_requests_total")

	assert.True(t, labels["/api/v1/users/:id"], "应记录路由模板，实际标签: %v", labels)
	for _, id := range []string{"/api/v1/users/7", "/api/v1/users/8", "/api/v1/users/9"} {
		assert.False(t, labels[id], "不应把真实路径写进标签: %s", id)
	}
}

// TestMetrics_UnmatchedRoute 验证未匹配的路径统一归到 unmatched。
// 扫描器乱打的路径同样会炸标签基数。
func TestMetrics_UnmatchedRoute(t *testing.T) {
	srv, _ := newTestServer(t, &stubUserService{})

	for _, p := range []string{"/nope-1", "/nope-2", "/.env"} {
		srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	labels := routeLabels(t, "http_requests_total")

	assert.True(t, labels["unmatched"])
	assert.False(t, labels["/nope-1"])
	assert.False(t, labels["/.env"])
}

func TestHTTP_SecurityHeaders(t *testing.T) {
	t.Run("默认下发基础安全头且不含 HSTS", func(t *testing.T) {
		srv := newServerWithConfig(t, nil)

		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))

		assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
		assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
		assert.NotEmpty(t, w.Header().Get("Content-Security-Policy"))
		// HSTS 默认关闭：HTTP 下无意义，且证书没配好时会导致全站不可用
		assert.Empty(t, w.Header().Get("Strict-Transport-Security"))
	})

	t.Run("显式开启后下发 HSTS", func(t *testing.T) {
		srv := newServerWithConfig(t, func(cfg *config.Config) {
			cfg.Server.EnableHSTS = true
		})

		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))

		assert.Contains(t, w.Header().Get("Strict-Transport-Security"), "max-age=")
	})
}

// TestHealthRegistry_Draining 验证摘流阶段 readiness 立刻变为不健康。
// 少了这一步，SIGTERM 之后 LB 仍会在下一次探测前继续转发流量，
// 而服务已经拒绝新连接 —— 表现为发布期间的一小批 502。
func TestHealthRegistry_Draining(t *testing.T) {
	reg := health.NewRegistry(time.Second, time.Minute)
	reg.RegisterFunc("fake", func(context.Context) error { return nil })

	require.True(t, reg.Check(context.Background()).Healthy)

	reg.StartDraining()
	report := reg.Check(context.Background())

	assert.True(t, reg.IsDraining())
	assert.False(t, report.Healthy)
	assert.True(t, report.Draining, "摘流应能与「依赖故障」区分开")
}

func TestAdmin_Endpoints(t *testing.T) {
	t.Run("暴露 metrics 与 version", func(t *testing.T) {
		srv := admin.New(admin.Options{Addr: "127.0.0.1:0", Pprof: false})
		require.NotNil(t, srv)

		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "http_requests_total")

		w = httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/version", nil))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "version")
	})

	t.Run("pprof 关闭时不可访问", func(t *testing.T) {
		srv := admin.New(admin.Options{Addr: "127.0.0.1:0", Pprof: false})

		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("pprof 开启时可访问", func(t *testing.T) {
		srv := admin.New(admin.Options{Addr: "127.0.0.1:0", Pprof: true})

		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("未配置地址时不启动", func(t *testing.T) {
		assert.Nil(t, admin.New(admin.Options{}))
		assert.NoError(t, admin.Shutdown(context.Background(), nil))
	})
}
