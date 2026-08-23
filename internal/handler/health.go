package handler

import (
	"net/http"
	"time"

	"myproject/pkg/buildinfo"
	"myproject/pkg/health"

	"github.com/gin-gonic/gin"
)

// HealthHandler 健康检查处理器
type HealthHandler struct {
	registry *health.Registry
}

// NewHealthHandler 创建健康检查处理器实例
func NewHealthHandler(registry *health.Registry) *HealthHandler {
	return &HealthHandler{registry: registry}
}

// Live 存活探针：只证明进程还在跑，不探测任何外部依赖。
// K8s liveness 用这个，避免下游抖动导致容器被反复重启。
// 同时回显版本号，便于确认线上跑的是哪个构建。
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"version":   buildinfo.Version,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Ready 就绪探针：探测所有依赖，任一不可用返回 503。
//
// 原实现无论数据库是否可用都返回 200，探针永远认为健康，
// 故障实例不会被摘除，流量会持续打进来。
//
// 退出流程中（收到 SIGTERM 后）也返回 503：先让 LB 摘流，
// 再关闭服务，避免摘流窗口内的请求被直接拒绝。
func (h *HealthHandler) Ready(c *gin.Context) {
	report := h.registry.Check(c.Request.Context())

	status := http.StatusOK
	statusText := "ok"
	switch {
	case report.Draining:
		status = http.StatusServiceUnavailable
		statusText = "draining"
	case !report.Healthy:
		status = http.StatusServiceUnavailable
		statusText = "unavailable"
	}

	c.JSON(status, gin.H{
		"status":    statusText,
		"version":   buildinfo.Version,
		"timestamp": time.Now().Format(time.RFC3339),
		"services":  report.Services,
	})
}

// Check 兼容旧的 /health 路径，语义等同 Ready
func (h *HealthHandler) Check(c *gin.Context) {
	h.Ready(c)
}
