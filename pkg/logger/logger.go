// Package logger 基于标准库 log/slog 的结构化日志。
//
// 基于标准库 log/slog（Go 1.21），不再自造 JSON 序列化与级别判断；
// 文件轮转加锁，避免多 goroutine 同时跨天轮转导致的 data race 与重复关闭文件句柄；
// 提供 ctx 贯穿能力：请求入口把 request_id 等字段绑到 logger 上塞进 ctx，
// 业务层用 logger.C(ctx) 取出，日志天然带上全链路字段。
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"time"
)

// Options 日志初始化参数
type Options struct {
	Level     string // debug / info / warn / error
	Format    string // json / console
	Dir       string // 日志目录，为空则仅写 stdout
	AddSource bool   // 是否记录调用位置
	// 保留策略（按小时轮转）。0 表示对应维度不限制。
	MaxBackups int // 保留的历史文件数
	MaxAgeDays int // 历史文件保留天数
}

var (
	defaultLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	fileWriter    *rotateWriter
)

// Init 初始化全局 logger。返回 error 让调用方决定是否降级为仅 stdout。
func Init(opts Options) error {
	var writers []io.Writer
	writers = append(writers, os.Stdout)

	if opts.Dir != "" {
		w, err := newRotateWriter(opts.Dir, retention{
			maxBackups: opts.MaxBackups,
			maxAge:     time.Duration(opts.MaxAgeDays) * 24 * time.Hour,
		})
		if err != nil {
			return fmt.Errorf("初始化日志文件失败: %w", err)
		}
		fileWriter = w
		writers = append(writers, w)
	}

	out := io.MultiWriter(writers...)
	// 时间字段默认是 RFC3339（2026-08-23T19:26:15.806+08:00），
	// 用 ReplaceAttr 改成 "年-月-日 时:分:秒"（空格分隔、不带 T 与时区偏移），
	// 更易读。这是 slog 标准库自定义时间格式的唯一官方途径
	// （HandlerOptions 本身没有 TimeFieldFormat 字段）。
	handlerOpts := &slog.HandlerOptions{
		Level:     parseLevel(opts.Level),
		AddSource: opts.AddSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format("2006-01-02 15:04:05"))
				}
			}
			return a
		},
	}

	var h slog.Handler
	if opts.Format == "console" {
		h = slog.NewTextHandler(out, handlerOpts)
	} else {
		h = slog.NewJSONHandler(out, handlerOpts)
	}

	defaultLogger = slog.New(h)
	return nil
}

// Close 关闭日志文件句柄
func Close() error {
	if fileWriter != nil {
		return fileWriter.Close()
	}
	return nil
}

// L 返回全局 logger
func L() *slog.Logger { return defaultLogger }

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ---------- ctx 贯穿 ----------

type ctxKey struct{}

// WithContext 把带字段的 logger 放入 ctx，供下游调用链复用
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// C 从 ctx 取出 logger，没有则返回全局 logger。
// 业务代码统一用 logger.C(ctx).Info(...)，日志自动带上 request_id 等字段。
func C(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return defaultLogger
	}
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return defaultLogger
}

// ---------- 兼容旧 map 风格 API ----------
// 老代码是 logger.Info("msg", map[string]interface{}{...})，保留这组函数避免全量改造，
// 新代码建议直接用 logger.C(ctx).Info("msg", "key", value)。

// fieldsToArgs 把 map 风格的字段摊平成 slog 的 key, value, ... 序列。
//
// 必须遍历全部 map：只取 fields[0] 的话，调用方多传一个 map（比如把公共字段
// 和业务字段分开传）会被静默丢弃，日志缺字段且没有任何报错，排查时极难发现。
//
// 每个 map 的 key 先排序再拼接：Go 的 map 遍历顺序随机，不排序的话同一句日志
// 每次输出的字段顺序都不同 —— 字段顺序不影响语义（日志字段是 key=value 对），
// 但影响可读性与 diff 稳定性：排查时拿两次日志做 diff、或用固定字符串 grep，
// 字段顺序一乱，比对结果就会抖动，掩盖真正的差异。
// 这里排序不改变「相对 map 之间的顺序」（仍按调用方传参顺序），只固定单个 map 内部顺序。
func fieldsToArgs(fields []map[string]interface{}) []any {
	total := 0
	for _, f := range fields {
		total += len(f)
	}
	if total == 0 {
		return nil
	}
	args := make([]any, 0, total*2)
	for _, f := range fields {
		keys := make([]string, 0, len(f))
		for k := range f {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, k, f[k])
		}
	}
	return args
}

// Debug 全局调试日志
func Debug(msg string, fields ...map[string]interface{}) {
	defaultLogger.Debug(msg, fieldsToArgs(fields)...)
}

// Info 全局信息日志
func Info(msg string, fields ...map[string]interface{}) {
	defaultLogger.Info(msg, fieldsToArgs(fields)...)
}

// Warn 全局警告日志
func Warn(msg string, fields ...map[string]interface{}) {
	defaultLogger.Warn(msg, fieldsToArgs(fields)...)
}

// Error 全局错误日志
func Error(msg string, fields ...map[string]interface{}) {
	defaultLogger.Error(msg, fieldsToArgs(fields)...)
}
