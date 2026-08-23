package middleware

import (
	"net/http"
	"strconv"
	"time"

	"myproject/pkg/metrics"

	"github.com/gin-gonic/gin"
)

// Metrics 采集 RED 指标。
//
// route 标签取 c.FullPath()（路由模板，如 /api/v1/users/:id）而不是真实路径：
// 用真实路径会让每个 ID 产生一条独立时间序列，指标基数无上限增长。
// 未匹配任何路由时 FullPath 为空，统一归到 "unmatched"，
// 否则扫描器乱打的路径同样会炸标签。method 同理做白名单收敛 ——
// 未匹配路由时方法名是调用方可控的任意 token。
//
// 收尾逻辑放在 defer 里：Recovery 注册在本中间件的内层，正常情况下它会把
// panic 转成 500，defer 里读到的就是真实状态码；即便 Recovery 原样再抛
// （http.ErrAbortHandler），in_flight 也不会只增不减。
// 注意 Recovery 必须在内层：放外层的话 panic 请求在这里读到的是 gin 的默认
// 状态 200，最需要告警的那类请求会被记成成功。
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		metrics.RequestsInFlight.Inc()

		defer func() {
			metrics.RequestsInFlight.Dec()

			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			method := normalizeMethod(c.Request.Method)

			metrics.RequestDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(method, route, strconv.Itoa(c.Writer.Status())).Inc()
			if size := c.Writer.Size(); size > 0 {
				metrics.ResponseSize.WithLabelValues(route).Observe(float64(size))
			}
		}()

		c.Next()
	}
}

// normalizeMethod 把非标准 HTTP 动词归到 other，避免标签基数被调用方左右
func normalizeMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return m
	default:
		return "other"
	}
}
