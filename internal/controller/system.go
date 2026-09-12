package controller

import (
	"net/http"
	"time"

	"myproject/pkg/buildinfo"
	"myproject/pkg/errcode"
	"myproject/pkg/health"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// 本文件是系统端点：探针与兜底路由。它们不属于任何业务模块，
// 也不经过 service 层，所以单独放在这里而不是混进 user.go。

// Live 存活探针：只证明进程还在跑，不探测任何外部依赖。
// K8s liveness 用这个，避免下游抖动导致容器被反复重启。
func Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"version":   buildinfo.Version,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Ready 就绪探针：探测所有依赖，任一不可用返回 503。
// 退出流程中（收到 SIGTERM 后）也返回 503：先让 LB 摘流再关闭服务。
func Ready(c *gin.Context) {
	report := health.Check(c.Request.Context())

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

// NotFound 未匹配路由。统一返回 JSON，避免客户端解析到 gin 默认的纯文本 404。
func NotFound(c *gin.Context) {
	response.Error(c, errcode.ErrNotFound.WithDetails("路由不存在: %s %s", c.Request.Method, c.Request.URL.Path))
}

// MethodNotAllowed 路径存在但 HTTP 方法不匹配。
// 需要 router 里开启 gin 的 HandleMethodNotAllowed，否则这类请求会被当成 404。
func MethodNotAllowed(c *gin.Context) {
	response.Error(c, errcode.ErrMethodNotAllowed.WithDetails("方法不允许: %s %s", c.Request.Method, c.Request.URL.Path))
}
