// Package admin 提供内部管理端点：指标、pprof、版本信息。
//
// 为什么单独起一个 Server 而不是挂在业务路由上：
//   - /metrics 会暴露路由清单、QPS、延迟分布，属于内部信息；
//   - /debug/pprof 能拉取堆和 CPU profile，既泄露实现细节，
//     又能被反复触发当成 DoS（profile 期间有额外开销）。
//
// 因此默认只监听 127.0.0.1，需要被 Prometheus 跨机抓取时再放开到内网地址，
// 并由防火墙/安全组限制来源，而不是靠在公网端口上加一层 token。
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"time"

	"myproject/pkg/buildinfo"
	"myproject/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Options admin 服务配置
type Options struct {
	Addr  string // 监听地址，建议 127.0.0.1:9090
	Pprof bool   // 是否开启 /debug/pprof
}

// New 构建 admin Server。返回 nil 表示未启用。
func New(opts Options) *http.Server {
	if opts.Addr == "" {
		return nil
	}

	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{
		// 采集自身出错时不要静默：写进响应体，抓取方能直接看到
		ErrorHandling: promhttp.HTTPErrorOnError,
	}))

	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version": buildinfo.Version,
			"go":      buildinfo.GoVersion(),
		})
	})

	if opts.Pprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	return &http.Server{
		Addr:    opts.Addr,
		Handler: mux,
		// pprof 的 CPU profile 默认采样 30 秒，写超时必须放宽，
		// 否则 profile 永远拉不完整。
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      2 * time.Minute,
	}
}

// Shutdown 关闭 admin 服务，忽略「本来就没启动」的情况
func Shutdown(ctx context.Context, srv *http.Server) error {
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
