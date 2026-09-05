// Package response 统一 HTTP 响应封装。
package response

import (
	"net/http"
	"sync/atomic"

	"myproject/pkg/errcode"
	"myproject/pkg/logger"

	"github.com/gin-gonic/gin"
)

// exposeDetails 是否把错误细节返回给客户端。
//
// 默认关闭：安全开关的默认值要取最保守的那一档。任何新入口
// （worker、另一个 cmd、测试）忘记调 SetExposeDetails 时，
// 结果应该是「少给信息」而不是「把内部细节吐出去」。
// 用 atomic 是因为设置发生在启动阶段、读取发生在每个请求的 goroutine 里。
var exposeDetails atomic.Bool

// SetExposeDetails 设置是否对外暴露错误细节（生产环境应为 false）
func SetExposeDetails(v bool) { exposeDetails.Store(v) }

// Response 统一响应结构
type Response struct {
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	Details   string      `json:"details,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
}

// ListData 列表数据响应
type ListData struct {
	List     interface{} `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// requestIDKey 与 middleware.RequestIDKey 保持一致，这里避免包循环依赖单独定义
const requestIDKey = "request_id"

func requestID(c *gin.Context) string {
	if v, ok := c.Get(requestIDKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Success 成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:      0,
		Message:   "success",
		Data:      data,
		RequestID: requestID(c),
	})
}

// SuccessList 成功响应（列表）
func SuccessList(c *gin.Context, list interface{}, total int64, page, pageSize int) {
	Success(c, ListData{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// Error 错误响应。
// 5xx 一律落日志：避免内部错误静默丢失。
func Error(c *gin.Context, err error) {
	if err == nil {
		Success(c, nil)
		return
	}

	e := errcode.From(err)

	if e.HTTPStatus() >= http.StatusInternalServerError {
		attrs := []any{
			"code", e.Code(),
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
			"error", e.Error(),
		}
		if cause := e.Cause(); cause != nil {
			attrs = append(attrs, "cause", cause.Error())
		}
		logger.C(c.Request.Context()).Error("request failed", attrs...)
	} else {
		logger.C(c.Request.Context()).Debug("request rejected",
			"code", e.Code(), "message", e.Message(), "details", e.Details())
	}

	resp := Response{
		Code:      e.Code(),
		Message:   e.Message(),
		RequestID: requestID(c),
	}
	// 4xx 的 details 描述的是调用方自己的输入（哪个字段不合法），
	// 生产也应该返回，否则前端拿不到可用信息；
	// 5xx 的 details 可能含内部实现细节，只在非生产环境暴露。
	if exposeDetails.Load() || e.HTTPStatus() < http.StatusInternalServerError {
		resp.Details = e.Details()
	}

	c.AbortWithStatusJSON(e.HTTPStatus(), resp)
}
