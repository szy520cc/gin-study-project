package middleware

import (
	"errors"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"myproject/pkg/errcode"
	"myproject/pkg/logger"
	"myproject/pkg/metrics"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// Recovery panic 恢复中间件。
//
// 替代 gin.Recovery()：默认实现只把堆栈打到 stderr，
// 既不进结构化日志、也不带 request_id，线上无法定位。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// http.ErrAbortHandler 是 net/http 约定的「静默中止」信号
				// （httputil 的反向代理、Hijack 场景会用），
				// 它不是程序 bug，也不该打堆栈、计指标或写响应 —— 原样上抛给 net/http。
				if e, ok := err.(error); ok && errors.Is(e, http.ErrAbortHandler) {
					panic(err)
				}

				// 客户端断开导致的 broken pipe 不算服务异常，降级为 warn 且不再写响应
				if isBrokenPipe(err) {
					logger.C(c.Request.Context()).Warn("client disconnected", "error", err)
					c.Abort()
					return
				}

				logger.C(c.Request.Context()).Error("panic recovered",
					"error", err,
					"stack", stackTrace(4),
					"client_ip", c.ClientIP(),
				)
				// 这个指标应该长期为 0，一旦不为 0 就该告警
				metrics.PanicsTotal.WithLabelValues("http").Inc()

				// 响应已经开始写出时不能再写一遍：那会产生
				// "superfluous WriteHeader" 并把 JSON 拼到已有响应体后面，
				// 客户端拿到的是一个坏掉的响应。
				if c.Writer.Written() {
					c.Abort()
					return
				}
				response.Error(c, errcode.ErrInternal)
			}
		}()

		c.Next()
	}
}

// isBrokenPipe 判断 panic 是否由客户端断连引起。
// 用 errors.As 而不是类型断言：被包装过的 *net.OpError 同样要能识别，
// 否则会被当成真 panic 打全栈 + 计指标。
func isBrokenPipe(err interface{}) bool {
	e, ok := err.(error)
	if !ok {
		return false
	}
	var ne *net.OpError
	if !errors.As(e, &ne) {
		return false
	}
	var se *os.SyscallError
	if !errors.As(ne.Err, &se) {
		return false
	}
	return errors.Is(se.Err, syscall.EPIPE) || errors.Is(se.Err, syscall.ECONNRESET)
}

// stackTrace 返回可读调用栈，跳过 skip 层框架帧，并过滤 runtime 内部帧
func stackTrace(skip int) string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	var sb strings.Builder
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.File, "/runtime/") {
			sb.WriteString(frame.Function)
			sb.WriteString("\n\t")
			sb.WriteString(frame.File)
			sb.WriteByte(':')
			sb.WriteString(strconv.Itoa(frame.Line))
			sb.WriteByte('\n')
		}
		if !more {
			break
		}
	}
	return sb.String()
}
