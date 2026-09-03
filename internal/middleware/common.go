package middleware

import "github.com/gin-gonic/gin"

// 本文件放中间件包的公共内容。各中间件自己的常量（如 auth.go 的 CtxUserID、
// requestid.go 的 RequestIDHeader）仍留在对应文件里，由使用它的能力自己拥有。

// NoOp 空操作中间件。用于「功能关闭时」占位，
// 避免调用方为「开/关」两种情况各写一条注册分支。
func NoOp() gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}
