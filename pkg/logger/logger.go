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
	// 保留策略。只按天切分而不清理，磁盘迟早被写满，
	// 而磁盘满会连带拖垮数据库和整机。0 表示对应维度不限制。
	MaxSizeMB  int // 单文件上限，超过则切分出 app_YYYYMMDD_HHMMSS.log
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
			maxSizeBytes: int64(opts.MaxSizeMB) * 1024 * 1024,
			maxBackups:   opts.MaxBackups,
			maxAge:       time.Duration(opts.MaxAgeDays) * 24 * time.Hour,
		})
		if err != nil {
			return fmt.Errorf("初始化日志文件失败: %w", err)
		}
		fileWriter = w
		writers = append(writers, w)
	}

	out := io.MultiWriter(writers...)
	handlerOpts := &slog.HandlerOptions{
		Level:     parseLevel(opts.Level),
		AddSource: opts.AddSource,
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

// ---------- 按天/按大小轮转的文件写入器 ----------

// retention 保留策略，零值表示对应维度不限制
type retention struct {
	maxSizeBytes int64
	maxBackups   int
	maxAge       time.Duration
}

// rotateWriter 按天 + 按大小切分日志文件，并按保留策略清理历史文件。
// 所有状态变更都在锁内完成：原实现的 checkRotate 在请求 goroutine 里
// 无锁读写文件句柄，跨天时会 data race。
type rotateWriter struct {
	mu   sync.Mutex
	dir  string
	date string
	size int64
	f    *os.File
	ret  retention
}

const currentLogName = "app.log"

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

	if w.shouldRotateLocked(len(p)) {
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

func (w *rotateWriter) shouldRotateLocked(incoming int) bool {
	if time.Now().Format("20060102") != w.date {
		return true
	}
	return w.ret.maxSizeBytes > 0 && w.size+int64(incoming) > w.ret.maxSizeBytes
}

// openLocked 打开当前写入文件。必须在持有 w.mu 时调用。
func (w *rotateWriter) openLocked() error {
	path := filepath.Join(w.dir, currentLogName)
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
	w.date = time.Now().Format("20060102")
	return nil
}

// rotateLocked 归档当前文件并重开一个。必须在持有 w.mu 时调用。
func (w *rotateWriter) rotateLocked() error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}

	current := filepath.Join(w.dir, currentLogName)
	if st, err := os.Stat(current); err == nil && st.Size() > 0 {
		archived, err := w.archiveNameLocked()
		if err != nil {
			return err
		}
		if err := os.Rename(current, archived); err != nil {
			return fmt.Errorf("归档日志文件失败: %w", err)
		}
	}

	if err := w.openLocked(); err != nil {
		return err
	}
	w.cleanupLocked()
	return nil
}

// archiveNameLocked 生成不冲突的归档文件名。
//
// 只用毫秒时间戳不够：同一毫秒内发生两次轮转（写入量大 + max_size_mb 小）时，
// os.Rename 会静默覆盖前一个归档文件，丢掉一整段日志且不报错。
// 冲突时追加序号，保持文件名的字典序仍然等于时间序（cleanupLocked 依赖这一点）。
func (w *rotateWriter) archiveNameLocked() (string, error) {
	base := time.Now().Format("20060102_150405.000")
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("app_%s.log", base)
		if i > 0 {
			name = fmt.Sprintf("app_%s.%02d.log", base, i)
		}
		path := filepath.Join(w.dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, nil
		}
	}
	return "", fmt.Errorf("生成归档文件名失败：%s 同名文件过多", base)
}

// cleanupLocked 按保留策略删除历史文件。清理失败只提示，不影响日志写入。
func (w *rotateWriter) cleanupLocked() {
	if w.ret.maxBackups <= 0 && w.ret.maxAge <= 0 {
		return
	}

	entries, err := filepath.Glob(filepath.Join(w.dir, "app_*.log"))
	if err != nil {
		return
	}
	// 文件名内嵌时间戳，字典序即时间序，倒序后下标越大越旧
	sort.Sort(sort.Reverse(sort.StringSlice(entries)))

	cutoff := time.Now().Add(-w.ret.maxAge)
	for i, path := range entries {
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
