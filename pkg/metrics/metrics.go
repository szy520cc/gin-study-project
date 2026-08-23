// Package metrics 定义应用指标（Prometheus）。
//
// 只暴露 RED 三要素 + 少量基础设施指标，不追求大而全：
//   - Rate：请求量（http_requests_total）
//   - Errors：错误率（同一个 counter 按 status 聚合即可算出）
//   - Duration：延迟分布（http_request_duration_seconds，可算 P95/P99）
//
// 关键约束：route 标签必须用路由模板（/api/v1/users/:id）而不是真实路径
// （/api/v1/users/123）。用真实路径会让每个 ID 产生一条时间序列，
// 指标基数无上限增长，先撑爆 Prometheus 再撑爆自己的内存。
package metrics

import (
	"database/sql"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Registry 应用自己的注册表。不用 prometheus.DefaultRegisterer：
// 全局注册表会被任何间接依赖悄悄写入，也无法在测试里隔离。
var Registry = prometheus.NewRegistry()

var (
	// RequestsTotal 请求总数。Errors 与 Rate 都从这里算。
	RequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP 请求总数",
	}, []string{"method", "route", "status"})

	// RequestDuration 请求耗时分布。桶按「Web API 常见延迟」布置，
	// 默认桶（最大 10s）对 API 场景偏粗。
	RequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP 请求耗时分布（秒）",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"method", "route"})

	// RequestsInFlight 正在处理的请求数。突增说明下游变慢或出现堆积。
	RequestsInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "当前正在处理的 HTTP 请求数",
	})

	// ResponseSize 响应体大小分布，用于发现意外的大响应
	ResponseSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_response_size_bytes",
		Help:    "HTTP 响应体大小分布（字节）",
		Buckets: prometheus.ExponentialBuckets(128, 4, 8),
	}, []string{"route"})

	// RateLimitRejected 限流拒绝数。scope 区分全局与认证接口配额。
	RateLimitRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ratelimit_rejected_total",
		Help: "被限流拒绝的请求数",
	}, []string{"scope"})

	// PanicsTotal panic 次数。这个指标应该长期为 0，一旦不为 0 就该告警。
	PanicsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "panics_recovered_total",
		Help: "被恢复的 panic 次数",
	}, []string{"source"})
)

func init() {
	Registry.MustRegister(
		RequestsTotal,
		RequestDuration,
		RequestsInFlight,
		ResponseSize,
		RateLimitRejected,
		PanicsTotal,
		// Go 运行时与进程指标：goroutine 数、GC、内存、FD、CPU
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// RegisterDBStats 采集 database/sql 连接池状态。
// 连接池打满是最常见的「服务变慢但看不出原因」的成因，
// WaitCount / WaitDuration 持续增长即为信号。
func RegisterDBStats(name string, statsFn func() sql.DBStats) error {
	labels := prometheus.Labels{"db": name}

	gauges := []struct {
		name  string
		help  string
		value func(sql.DBStats) float64
	}{
		{"db_pool_open_connections", "当前已建立的连接数", func(s sql.DBStats) float64 { return float64(s.OpenConnections) }},
		{"db_pool_in_use_connections", "正在使用的连接数", func(s sql.DBStats) float64 { return float64(s.InUse) }},
		{"db_pool_idle_connections", "空闲连接数", func(s sql.DBStats) float64 { return float64(s.Idle) }},
		{"db_pool_max_open_connections", "连接数上限", func(s sql.DBStats) float64 { return float64(s.MaxOpenConnections) }},
		{"db_pool_wait_count_total", "等待连接的累计次数", func(s sql.DBStats) float64 { return float64(s.WaitCount) }},
		{"db_pool_wait_duration_seconds_total", "等待连接的累计耗时", func(s sql.DBStats) float64 { return s.WaitDuration.Seconds() }},
	}

	for _, g := range gauges {
		g := g
		fn := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        g.name,
			Help:        g.help,
			ConstLabels: labels,
		}, func() float64 { return g.value(statsFn()) })

		if err := Registry.Register(fn); err != nil {
			return err
		}
	}
	return nil
}
