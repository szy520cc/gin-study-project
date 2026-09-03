// Package logger 基于标准库 log/slog 的结构化日志。
//
// 相比自研实现，这里做了三件事：
//  1. 底座换成 slog（Go 1.21 标准库），去掉自造的 JSON 序列化与级别判断；
//  2. 文件轮转加锁，修复了原实现中多 goroutine 同时跨天轮转导致的 data race
//     与重复关闭文件句柄的问题；
//  3. 提供 ctx 贯穿能力：请求入口把 request_id 等字段绑到 logger 上塞进 ctx，
//     业务层用 logger.C(ctx) 取出，日志天然带上全链路字段。
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
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

func fieldsToArgs(fields []map[string]interface{}) []any {
	if len(fields) == 0 {
		return nil
	}
	args := make([]any, 0, len(fields[0])*2)
	for k, v := range fields[0] {
		args = append(args, k, v)
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

// Fatal 记录后退出进程。注意会跳过 defer，调用前请确保资源已释放。
func Fatal(msg string, fields ...map[string]interface{}) {
	defaultLogger.Error(msg, fieldsToArgs(fields)...)
	_ = Close()
	os.Exit(1)
}
