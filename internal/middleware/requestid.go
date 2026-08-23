package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"myproject/pkg/logger"

	"github.com/gin-gonic/gin"
)

const (
	// RequestIDKey gin.Context 中的 key
	RequestIDKey = "request_id"
	// RequestIDHeader 请求/响应头名称
	RequestIDHeader = "X-Request-ID"
)

// RequestID 为每个请求生成（或透传上游的）唯一标识，并绑定到 ctx 中的 logger。
//
// 这是全链路排查的地基：绑定之后，handler / service / repository 里
// 任何 logger.C(ctx).Info(...) 都会自动带上 request_id，
// 无需手工层层传参。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 客户端传入的值要校验后才敢用：它会进日志、也会回显到响应头。
		// 不限长会让一个超长 ID 乘上该请求的所有日志条数；
		// 不限字符集会让日志里出现难以检索的控制字符。
		id := sanitizeRequestID(c.GetHeader(RequestIDHeader))
		if id == "" {
			id = newRequestID()
		}

		c.Set(RequestIDKey, id)
		c.Header(RequestIDHeader, id)

		l := logger.L().With(
			"request_id", id,
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
		)
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context(), l))

		c.Next()
	}
}

// maxRequestIDLen 透传的 request id 长度上限
const maxRequestIDLen = 64

// sanitizeRequestID 只接受 [A-Za-z0-9._-] 且长度合规的值，否则返回空串（交由上层重新生成）
func sanitizeRequestID(id string) string {
	if id == "" || len(id) > maxRequestIDLen {
		return ""
	}
	for i := 0; i < len(id); i++ {
		ch := id[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '.', ch == '_', ch == '-':
		default:
			return ""
		}
	}
	return id
}

// 不提供 GetRequestID(c) 包装：原来那个函数零调用，
// 而 c.GetString(RequestIDKey) 本身就是一行。
// 需要在 handler 里拿 request_id 时直接用 RequestIDKey。

func newRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败是极端情况，但不能退化成空串：
		// 空 request_id 会让这条请求在日志里彻底失联，响应头也会是空值。
		// 用纳秒时间戳 + 自增序号兜底，仍然能在日志里定位到单条请求。
		return fmt.Sprintf("fallback-%d-%d", time.Now().UnixNano(), fallbackSeq.Add(1))
	}
	return hex.EncodeToString(b)
}

// fallbackSeq 兜底 ID 的自增序号，避免同一纳秒内重复
var fallbackSeq atomic.Uint64
