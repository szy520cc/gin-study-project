package middleware

import (
	"net/http"

	"myproject/pkg/errcode"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// BodyLimit 限制请求体大小。
//
// http.Server.MaxHeaderBytes 只约束 header，body 不设限时一个大 JSON
// 就能把进程内存打满（ShouldBindJSON 会把整个 body 读进内存）。
//
// 两道防线：
//  1. Content-Length 已声明且超限 —— 直接拒绝，不读一个字节；
//  2. 未声明（chunked）或声明造假 —— 用 MaxBytesReader 在读取过程中截断，
//     handler 侧 bindJSON 会把 *http.MaxBytesError 映射成 413。
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		return NoOp()
	}

	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			response.Error(c, errcode.ErrBodyTooLarge.WithDetails("请求体上限 %d 字节", maxBytes))
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
