package handler

import (
	"myproject/pkg/errcode"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// NotFound 未匹配路由。统一返回 JSON，避免客户端解析到 gin 默认的纯文本 404。
func NotFound(c *gin.Context) {
	response.Error(c, errcode.ErrNotFound.WithDetails("路由不存在: %s %s", c.Request.Method, c.Request.URL.Path))
}

// MethodNotAllowed 路径存在但 HTTP 方法不匹配。
// 需要 router 里开启 gin 的 HandleMethodNotAllowed，否则这类请求会被当成 404。
func MethodNotAllowed(c *gin.Context) {
	response.Error(c, errcode.ErrMethodNotAllowed.WithDetails("方法不允许: %s %s", c.Request.Method, c.Request.URL.Path))
}
