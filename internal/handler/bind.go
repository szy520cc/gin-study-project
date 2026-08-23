package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"myproject/pkg/errcode"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

// InitValidator 让校验错误里的字段名用 json/form tag 而不是 Go 结构体字段名，
// 客户端拿到的 "email" 才对得上自己发的请求。由 router.Setup 调用一次。
func InitValidator() {
	v, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return
	}
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		for _, tag := range []string{"json", "form"} {
			name := strings.SplitN(f.Tag.Get(tag), ",", 2)[0]
			if name != "" && name != "-" {
				return name
			}
		}
		return f.Name
	})
}

// bindJSON 绑定并校验 JSON 请求体。返回 false 时响应已写出，handler 直接 return。
//
// 收敛三件此前散落在每个 handler 里的事：
//  1. 重复的 ShouldBindJSON + ErrInvalidParams 样板；
//  2. 请求体超限识别 —— MaxBytesReader 触发时应返回 413 而不是 400；
//  3. 校验失败的错误信息翻译 —— 原来把 go-playground 的英文原串直接吐给客户端，
//     既没法用，还暴露了内部结构体名（如 UserRegisterRequest.Email）。
func bindJSON[T any](c *gin.Context, req *T) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		response.Error(c, bindError(err))
		return false
	}
	return true
}

// bindQuery 绑定并校验 query 参数
func bindQuery[T any](c *gin.Context, req *T) bool {
	if err := c.ShouldBindQuery(req); err != nil {
		response.Error(c, bindError(err))
		return false
	}
	return true
}

func bindError(err error) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return errcode.ErrBodyTooLarge.WithDetails("请求体上限 %d 字节", maxBytesErr.Limit)
	}

	var validationErrs validator.ValidationErrors
	if errors.As(err, &validationErrs) {
		return errcode.ErrInvalidParams.WithDetails("%s", formatValidationErrors(validationErrs))
	}

	// 类型不匹配（如 {"total_amount_cents":"abc"}）走的是 json 包的错误，
	// 它的 Error() 里带 Go 结构体名：
	//   json: cannot unmarshal string into Go struct field CreateOrderRequest.total_amount_cents of type int64
	// 只回字段名和期望类型，不把内部结构体名吐出去。
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if idx := strings.LastIndex(field, "."); idx >= 0 {
			field = field[idx+1:]
		}
		if field == "" {
			return errcode.ErrInvalidParams.WithDetails("请求体字段类型不正确，期望 %s", typeErr.Type.String())
		}
		return errcode.ErrInvalidParams.WithDetails("字段 %s 类型不正确，期望 %s", field, typeErr.Type.String())
	}

	// JSON 本身不合法（括号不闭合、截断等），同样不回原始错误串
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return errcode.ErrInvalidParams.WithDetails("请求体不是合法的 JSON")
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return errcode.ErrInvalidParams.WithDetails("请求体不完整或为空")
	}

	// query/form 的类型错误来自 gin 的 form 映射，它把 strconv 的错误原样上抛：
	//   strconv.ParseInt: parsing "abc": invalid syntax
	// 只取出字段值提示格式不对，不把标准库函数名和位宽吐给调用方。
	var numErr *strconv.NumError
	if errors.As(err, &numErr) {
		return errcode.ErrInvalidParams.WithDetails("参数 %q 不是合法的数字", numErr.Num).WithCause(err)
	}

	// 兜底：原始错误串一律只进日志（WithCause），不进响应体 ——
	// 它可能带 Go 结构体名、标准库函数名等实现细节，对调用方也不可读。
	return errcode.ErrInvalidParams.WithCause(err)
}

// formatValidationErrors 把校验错误翻成人能看懂的中文，字段名用 json tag（对齐 API 契约）
func formatValidationErrors(errs validator.ValidationErrors) string {
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Field()+" "+ruleMessage(e))
	}
	return strings.Join(msgs, "; ")
}

func ruleMessage(e validator.FieldError) string {
	switch e.Tag() {
	case "required":
		return "不能为空"
	case "email":
		return "必须是合法的邮箱地址"
	case "url":
		return "必须是合法的 URL"
	case "min":
		return fmt.Sprintf("不能小于 %s", e.Param())
	case "max":
		return fmt.Sprintf("不能大于 %s", e.Param())
	case "len":
		return fmt.Sprintf("长度必须为 %s", e.Param())
	case "gt":
		return fmt.Sprintf("必须大于 %s", e.Param())
	case "gte":
		return fmt.Sprintf("必须大于或等于 %s", e.Param())
	case "lt":
		return fmt.Sprintf("必须小于 %s", e.Param())
	case "lte":
		return fmt.Sprintf("必须小于或等于 %s", e.Param())
	case "oneof":
		return fmt.Sprintf("必须是 [%s] 之一", e.Param())
	default:
		return fmt.Sprintf("不满足规则 %s", e.Tag())
	}
}
