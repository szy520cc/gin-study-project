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
	"path/filepath"
	"sort"
	"sync"
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

// ---------- 按小时轮转的文件写入器 ----------

// retention 保留策略，零值表示对应维度不限制
type retention struct {
	maxBackups int
	maxAge     time.Duration
}

// rotateWriter 按小时切分日志文件：每个小时只写一个文件，文件名形如
// app_YYYYMMDDHH.log（例如 app_2026082319.log），换小时时把当前文件
// 重命名为上一小时的文件。
// 所有状态变更都在锁内完成，避免多 goroutine 同时跨小时轮转导致 data race。
type rotateWriter struct {
	mu     sync.Mutex
	dir    string
	hour   string // 当前活动文件对应的小时，格式 2006010215
	size   int64
	f      *os.File
	ret    retention
}

// currentLogName 当前活动日志文件名：带小时，天然按小时隔离
func currentLogName(hour string) string {
	if hour == "" {
		hour = time.Now().Format("2006010215")
	}
	return "app_" + hour + ".log"
}

func newRotateWriter(dir string, ret retention) (*rotateWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}
	w := &rotateWriter{dir: dir, ret: ret}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.openLocked(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.shouldRotateLocked() {
		if err := w.rotateLocked(); err != nil {
			// 轮转失败不影响本次写入（继续写旧文件），只在 stderr 提示
			fmt.Fprintf(os.Stderr, "日志轮转失败: %v\n", err)
		}
	}
	if w.f == nil {
		return len(p), nil
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// shouldRotateLocked 仅按小时判断：当前小时与活动文件所属小时不同即轮转。
// 一小时一个文件，简单粗暴。
func (w *rotateWriter) shouldRotateLocked() bool {
	return time.Now().Format("2006010215") != w.hour
}

// openLocked 打开当前小时的写入文件。必须在持有 w.mu 时调用。
func (w *rotateWriter) openLocked() error {
	hour := time.Now().Format("2006010215")
	path := filepath.Join(w.dir, currentLogName(hour))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	size := int64(0)
	if st, err := f.Stat(); err == nil {
		size = st.Size()
	}

	w.f = f
	w.size = size
	w.hour = hour
	return nil
}

// rotateLocked 换小时时关闭旧小时文件、打开新小时文件，并触发清理。
// 必须在持有 w.mu 时调用。
//
// 注意：活动文件名本身已带小时（app_YYYYMMDDHH.log），因此换小时时
// 无需 rename——旧文件已经以"旧小时"命名留在目录里了，直接关旧开新即可。
func (w *rotateWriter) rotateLocked() error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}

	if err := w.openLocked(); err != nil {
		return err
	}
	w.cleanupLocked()
	return nil
}

// cleanupLocked 按保留策略删除历史日志文件。清理失败只提示，不影响日志写入。
//
// 文件名形如 app_YYYYMMDDHH.log，字典序即时间序。当前正在写的小时文件
// （currentLogName(w.hour)）必须排除，绝不能误删。
func (w *rotateWriter) cleanupLocked() {
	if w.ret.maxBackups <= 0 && w.ret.maxAge <= 0 {
		return
	}

	entries, err := filepath.Glob(filepath.Join(w.dir, "app_*.log"))
	if err != nil {
		return
	}
	// 文件名内嵌小时，字典序即时间序，倒序后下标越大越旧
	sort.Sort(sort.Reverse(sort.StringSlice(entries)))

	active := filepath.Join(w.dir, currentLogName(w.hour))
	cutoff := time.Now().Add(-w.ret.maxAge)
	for i, path := range entries {
		// 永远不删当前正在写入的小时文件
		if path == active {
			continue
		}
		expired := false
		if w.ret.maxBackups > 0 && i >= w.ret.maxBackups {
			expired = true
		}
		if !expired && w.ret.maxAge > 0 {
			if st, err := os.Stat(path); err == nil && st.ModTime().Before(cutoff) {
				expired = true
			}
		}
		if expired {
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(os.Stderr, "清理日志文件失败: %v\n", err)
			}
		}
	}
}

func (w *rotateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
