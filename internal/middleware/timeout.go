package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// Timeout 给每个请求的 context 加上截止时间。
//
// 只设置 deadline、不额外起 goroutine 抢写响应：
// gin 的 ResponseWriter 不是并发安全的，超时 goroutine 直接写响应
// 会与业务 handler 争抢，产生「superfluous WriteHeader」和数据竞争。
//
// 生效前提是下游都尊重 ctx —— 本项目 repository 全部走 conn(ctx)，
// GORM 会把 ctx 传给 database/sql，超时后 SQL 会被取消。
func Timeout(d time.Duration) gin.HandlerFunc {
	if d <= 0 {
		// 未配置超时时返回空操作中间件，避免调用方分支判断
		return NoOp()
	}

	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
