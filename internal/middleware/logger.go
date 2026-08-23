package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"myproject/pkg/logger"

	"github.com/gin-gonic/gin"
)

// 敏感字段（脱敏用）。正则预编译，避免每个请求重复编译。
var sensitiveFields = []string{"password", "token", "secret", "authorization", "credential", "id_card", "phone"}

var sensitivePatterns = func() []*regexp.Regexp {
	ps := make([]*regexp.Regexp, 0, len(sensitiveFields))
	for _, f := range sensitiveFields {
		ps = append(ps, regexp.MustCompile(`(?i)"`+f+`"\s*:\s*"[^"]*"`))
	}
	return ps
}()

// 这些路径不记录访问日志，避免探针和静态请求淹没日志
var skipPaths = map[string]struct{}{
	"/health": {}, "/livez": {}, "/readyz": {}, "/metrics": {}, "/favicon.ico": {},
}

// LoggerConfig 日志中间件配置
type LoggerConfig struct {
	// LogBody 记录请求/响应体。默认关闭：开启后每个响应都会在内存中多存一份副本。
	LogBody bool
	// MaxBodySize 记录的最大字节数，超出截断
	MaxBodySize int
}

// LoggerWithConfig 带配置的请求日志中间件。
// 没有提供无参的 Logger() 包装：LogBody 必须由配置驱动，
// 留一个「默认配置」入口只会让人以为可以随手用。
func LoggerWithConfig(cfg LoggerConfig) gin.HandlerFunc {
	if cfg.MaxBodySize <= 0 {
		cfg.MaxBodySize = 2048
	}

	return func(c *gin.Context) {
		if _, skip := skipPaths[c.Request.URL.Path]; skip {
			c.Next()
			return
		}

		start := time.Now()
		query := c.Request.URL.RawQuery

		var requestBody []byte
		var blw *bodyWriter

		// 仅在需要时才读请求体、包装响应体。
		// 原实现无条件缓冲整个响应，大响应会双倍占用内存，且破坏流式输出。
		if cfg.LogBody {
			if c.Request.Body != nil {
				// 只截断「记进日志的那一份」，Body 本身必须完整交给 handler：
				// 直接用截断副本替换 Body 会让超过上限的合法 JSON 变成 unexpected EOF。
				// 外层 BodyLimit 已用 MaxBytesReader 包过 Body，下面的 joinedBody
				// 必须保留对它的引用，超限保护才不会在读走头部之后失效。
				head, _ := io.ReadAll(io.LimitReader(c.Request.Body, int64(cfg.MaxBodySize)+1))
				requestBody = head
				original := c.Request.Body
				c.Request.Body = &joinedBody{
					Reader: io.MultiReader(bytes.NewReader(head), original),
					closer: original,
				}
			}
			blw = &bodyWriter{ResponseWriter: c.Writer, body: bytes.NewBuffer(nil), limit: cfg.MaxBodySize}
			c.Writer = blw
		}

		// 收尾放进 defer：Recovery 注册在本中间件的内层，正常情况下 panic 会被它
		// 转成 500 后正常返回，但 Recovery 会原样再抛 http.ErrAbortHandler。
		// 不用 defer 的话那类请求连一条访问日志都不会留下。
		defer func() {
			attrs := []any{
				"status", c.Writer.Status(),
				"latency_ms", time.Since(start).Milliseconds(),
				"ip", c.ClientIP(),
				"size", c.Writer.Size(),
				"user_agent", c.Request.UserAgent(),
			}
			if query != "" {
				attrs = append(attrs, "query", maskQuery(query))
			}
			if len(c.Errors) > 0 {
				attrs = append(attrs, "gin_errors", c.Errors.ByType(gin.ErrorTypePrivate).String())
			}
			if cfg.LogBody {
				if len(requestBody) > 0 {
					attrs = append(attrs, "request_body", maskSensitive(truncate(string(requestBody), cfg.MaxBodySize)))
				}
				if blw != nil && blw.body.Len() > 0 {
					attrs = append(attrs, "response_body", maskSensitive(truncate(blw.body.String(), cfg.MaxBodySize)))
				}
			}

			l := logger.C(c.Request.Context())
			switch {
			case c.Writer.Status() >= http.StatusInternalServerError:
				l.Error("http request", attrs...)
			case c.Writer.Status() >= http.StatusBadRequest:
				l.Warn("http request", attrs...)
			default:
				l.Info("http request", attrs...)
			}
		}()

		c.Next()
	}
}

// joinedBody 把「已读走的头部」和「剩余未读部分」重新拼成完整的请求体。
// Close 转发给原始 Body，避免 net/http 无法回收连接。
type joinedBody struct {
	io.Reader
	closer io.Closer
}

func (b *joinedBody) Close() error { return b.closer.Close() }

// bodyWriter 仅在开启 LogBody 时使用，带上限，且透传 Flush 以兼容 SSE/流式响应
type bodyWriter struct {
	gin.ResponseWriter
	body  *bytes.Buffer
	limit int
}

func (w *bodyWriter) Write(b []byte) (int, error) {
	if w.body.Len() < w.limit {
		remain := w.limit - w.body.Len()
		if remain > len(b) {
			remain = len(b)
		}
		w.body.Write(b[:remain])
	}
	return w.ResponseWriter.Write(b)
}

func (w *bodyWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *bodyWriter) Flush() {
	w.ResponseWriter.Flush()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// maskQuery 对 query string 里的敏感参数做脱敏。
// body 早就过了脱敏，query 之前没过 —— 扫描器打过来的
// `?username=x&password=y&accessToken=z` 会被整条原样写进日志文件。
// 解析失败时不返回原串，宁可丢可读性也不落明文。
func maskQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "(unparsable query, masked)"
	}
	for key, vs := range values {
		if !isSensitiveKey(key) {
			continue
		}
		for i := range vs {
			vs[i] = "***"
		}
	}
	return values.Encode()
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, f := range sensitiveFields {
		// 子串匹配：access_token、user_password 这类变体也要覆盖
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}

// maskSensitive 对 JSON 中的敏感字段做脱敏
func maskSensitive(data string) string {
	if !strings.Contains(data, ":") {
		return data
	}
	for _, p := range sensitivePatterns {
		data = p.ReplaceAllStringFunc(data, func(m string) string {
			idx := strings.Index(m, ":")
			return m[:idx+1] + `"***"`
		})
	}
	return data
}
