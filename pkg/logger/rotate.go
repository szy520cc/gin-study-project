package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// 本文件是 logger 包的文件落盘实现：按小时轮转 + 保留策略。
// 与 logger.go 分开是因为它跟 slog 没有关系 —— 它只是一个 io.Writer。

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
	mu   sync.Mutex
	dir  string
	hour string // 当前活动文件对应的小时，格式 2006010215
	size int64
	f    *os.File
	ret  retention
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
	// kept 只数「保留的历史文件」：不能用 entries 的下标代替，
	// 当前活动文件也占一个下标，用下标计数会少保留一个历史文件。
	kept := 0
	for _, path := range entries {
		// 永远不删当前正在写入的小时文件
		if path == active {
			continue
		}
		expired := false
		if w.ret.maxBackups > 0 && kept >= w.ret.maxBackups {
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
			continue
		}
		kept++
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
