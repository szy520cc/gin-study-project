package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 自研的轮转与清理必须有用例兜住：只切分不清理会把磁盘写满，
// 而清理逻辑写错会误删当前正在写的文件。

func archived(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "app_*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestRotateWriter_RotatesBySize(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotateWriter(dir, retention{maxSizeBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	line := []byte(strings.Repeat("x", 40) + "\n")
	for i := 0; i < 5; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatal(err)
		}
	}

	if got := len(archived(t, dir)); got == 0 {
		t.Fatalf("超过 maxSizeBytes 后应产生归档文件，实际 %d 个", got)
	}
	if _, err := os.Stat(filepath.Join(dir, currentLogName)); err != nil {
		t.Fatalf("当前日志文件应始终存在: %v", err)
	}
}

func TestRotateWriter_TrimsToMaxBackups(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotateWriter(dir, retention{maxSizeBytes: 32, maxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 8; i++ {
		if _, err := w.Write([]byte(strings.Repeat("y", 40) + "\n")); err != nil {
			t.Fatal(err)
		}
		// 归档文件名精度到毫秒，避免同一毫秒内重名互相覆盖
		time.Sleep(2 * time.Millisecond)
	}

	if got := len(archived(t, dir)); got > 2 {
		t.Fatalf("归档文件数应被裁剪到 maxBackups=2，实际 %d 个", got)
	}
}

func TestRotateWriter_DropsExpiredByAge(t *testing.T) {
	dir := t.TempDir()

	stale := filepath.Join(dir, "app_20200101_000000.000.log")
	if err := os.WriteFile(stale, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, time.Now().Add(-72*time.Hour), time.Now().Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}

	w, err := newRotateWriter(dir, retention{maxSizeBytes: 32, maxAge: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// 触发一次轮转，清理在轮转后执行
	for i := 0; i < 3; i++ {
		if _, err := w.Write([]byte(strings.Repeat("z", 40) + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("超过 maxAge 的归档文件应被删除，err=%v", err)
	}
}

func TestRotateWriter_NoRetentionKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotateWriter(dir, retention{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 20; i++ {
		if _, err := w.Write([]byte(strings.Repeat("w", 100) + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	// 未配置上限时不应发生按大小的轮转
	if got := len(archived(t, dir)); got != 0 {
		t.Fatalf("未配置 maxSizeBytes 时不应轮转，实际归档 %d 个", got)
	}
}
