// Package errcode 定义业务错误码体系。
//
// 设计要点：
//  1. 支持 Unwrap/Is，可用 fmt.Errorf("...: %w", err) 包装底层错误后仍能判断类型；
//  2. WithCause 保留底层错误用于日志排查，但不返回给客户端；
//  3. Details 可被 response 层读取，非生产环境返回给调用方，便于联调。
package errcode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// StatusClientClosedRequest 客户端主动断开。非标准状态码（nginx 约定），
// 用于把「客户端取消」与「服务端出错」在日志和监控里区分开。
const StatusClientClosedRequest = 499

// Error 业务错误。不可变：WithDetails/WithCause 均返回副本，
// 因此包级预定义的错误变量可以安全地被并发复用。
type Error struct {
	code       int
	message    string
	details    string
	httpStatus int
	cause      error
}

// New 创建错误
func New(code int, message string, httpStatus int) *Error {
	return &Error{code: code, message: message, httpStatus: httpStatus}
}

// Code 业务错误码
func (e *Error) Code() int { return e.code }

// Message 面向用户的错误信息
func (e *Error) Message() string { return e.message }

// Details 附加细节（如参数校验的具体原因）
func (e *Error) Details() string { return e.details }

// HTTPStatus HTTP 状态码
func (e *Error) HTTPStatus() int { return e.httpStatus }

// Cause 底层错误
func (e *Error) Cause() error { return e.cause }

// Error 实现 error 接口
func (e *Error) Error() string {
	msg := fmt.Sprintf("code: %d, message: %s", e.code, e.message)
	if e.details != "" {
		msg += ", details: " + e.details
	}
	if e.cause != nil {
		msg += ", cause: " + e.cause.Error()
	}
	return msg
}

// Unwrap 支持 errors.Is / errors.As 穿透到底层错误
func (e *Error) Unwrap() error { return e.cause }

// Is 让包装过 details/cause 的副本仍能被 errors.Is 判定为同一业务错误
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.code == t.code
}

// WithDetails 附加细节，返回副本
func (e *Error) WithDetails(format string, args ...interface{}) *Error {
	c := *e
	if len(args) == 0 {
		c.details = format
	} else {
		c.details = fmt.Sprintf(format, args...)
	}
	return &c
}

// WithCause 附加底层错误，返回副本
func (e *Error) WithCause(err error) *Error {
	c := *e
	c.cause = err
	return &c
}

// From 把任意 error 转成 *Error。
//
// 优先级：
//  1. 已是业务错误则原样返回；
//  2. context 超时/取消单独识别 —— 否则请求超时会被兜成 500「内部错误」，
//     日志刷 error、告警误报，排查方向被带偏；
//  3. 其余包装成内部错误。
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var be *Error
	if errors.As(err, &be) {
		return be
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTimeout.WithCause(err)
	case errors.Is(err, context.Canceled):
		return ErrClientClosed.WithCause(err)
	}
	return ErrInternal.WithCause(err)
}

// 通用错误
var (
	ErrInternal      = New(10001, "内部错误", http.StatusInternalServerError)
	ErrInvalidParams = New(10002, "参数错误", http.StatusBadRequest)
	ErrNotFound      = New(10003, "资源不存在", http.StatusNotFound)
	ErrUnauthorized  = New(10004, "未授权", http.StatusUnauthorized)
	ErrTooManyReq    = New(10006, "请求过于频繁", http.StatusTooManyRequests)
	ErrTimeout       = New(10007, "请求处理超时", http.StatusGatewayTimeout)
	// ErrMethodNotAllowed 路径存在但方法不匹配。需要 gin 开启 HandleMethodNotAllowed 才会触发。
	ErrMethodNotAllowed = New(10008, "方法不允许", http.StatusMethodNotAllowed)
	// ErrClientClosed 客户端主动断开。响应写不出去了，返回码只用于日志/监控归类。
	ErrClientClosed = New(10009, "客户端已断开", StatusClientClosedRequest)
	// ErrBodyTooLarge 请求体超过限制
	ErrBodyTooLarge = New(10010, "请求体过大", http.StatusRequestEntityTooLarge)
)

// 认证相关错误
var (
	ErrTokenNotFound = New(20001, "Token 不存在", http.StatusUnauthorized)
	ErrTokenInvalid  = New(20002, "Token 无效", http.StatusUnauthorized)
	ErrTokenExpired  = New(20003, "Token 已过期", http.StatusUnauthorized)
	ErrTokenGenerate = New(20004, "Token 生成失败", http.StatusInternalServerError)
)

// 用户相关错误
var (
	ErrUserNotFound      = New(30001, "用户不存在", http.StatusNotFound)
	ErrUserAlreadyExist  = New(30002, "用户已存在", http.StatusBadRequest)
	ErrEmailAlreadyExist = New(30003, "邮箱已被注册", http.StatusBadRequest)
	ErrPasswordIncorrect = New(30004, "密码错误", http.StatusBadRequest)
	ErrUserDisabled      = New(30005, "用户已被禁用", http.StatusForbidden)
)

// 订单相关错误
var (
	ErrOrderNotFound = New(40001, "订单不存在", http.StatusNotFound)
	// ErrInvalidOrderStatus 订单状态流转非法或并发冲突，用 409 而非 400。
	//
	// 它承担的两类触发场景都是「客户端请求与资源当前状态冲突」，正是 HTTP 409 Conflict
	// 的标准语义：
	//  1. 状态流转不合法（如已支付不能再变回待支付、跳过中间态直接完成）；
	//  2. 并发下原状态已被别的操作改掉（UpdateStatus 乐观锁 RowsAffected==0，统一转成这个错误）。
	// 用 400 会把它和「参数格式错误」混为一谈，误导调用方；409 才能正确表达「请重新查询当前状态后重试」。
	ErrInvalidOrderStatus = New(40002, "无效的订单状态", http.StatusConflict)
	ErrOrderCannotDelete  = New(40003, "订单无法删除", http.StatusBadRequest)
)

// 项目相关错误
var (
	ErrProjectNotFound      = New(50001, "项目不存在", http.StatusNotFound)
	ErrProjectLogoExists    = New(50002, "项目标识已存在", http.StatusBadRequest)
)

// 字段相关错误
var (
	ErrFieldNotFound            = New(60001, "字段不存在", http.StatusNotFound)
	ErrFieldParsePathExists     = New(60002, "字段解析路径已存在", http.StatusBadRequest)
	ErrFieldTypeInvalid         = New(60003, "字段类型不合法", http.StatusBadRequest)
	ErrFieldDefaultValueInvalid = New(60004, "字段默认值不合法", http.StatusBadRequest)
	ErrFieldDefaultTypeMismatch = New(60005, "字段默认值类型与字段类型不一致", http.StatusBadRequest)
)

// 配置包相关错误
var (
	ErrConfigPackNotFound   = New(70001, "配置包不存在", http.StatusNotFound)
	ErrConfigPackLogoExists = New(70002, "配置包标识已存在", http.StatusBadRequest)
)

// 配置数据相关错误
var (
	ErrConfigNotFound         = New(80001, "配置不存在", http.StatusNotFound)
	ErrConfigLogoVersionExists = New(80002, "配置版本已存在", http.StatusBadRequest)
)
