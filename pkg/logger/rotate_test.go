package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 自研的按小时轮转与清理必须有用例兜住：只切分不清理会把磁盘写满，
// 而清理逻辑写错会误删当前正在写的小时文件。

func archived(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "app_*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// 文件名必须带小时，形如 app_YYYYMMDDHH.log
func TestCurrentLogName_HasHour(t *testing.T) {
	name := currentLogName("2026082319")
	if name != "app_2026082319.log" {
		t.Fatalf("期望 app_2026082319.log，实际 %s", name)
	}
	// 默认用当前小时
	now := time.Now().Format("2006010215")
	if currentLogName("") != "app_"+now+".log" {
		t.Fatalf("默认小时文件名不符预期: %s", currentLogName(""))
	}
}

// 同小时内不轮转：持续写入只产生一个文件
func TestRotateWriter_NoRotateWithinHour(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotateWriter(dir, retention{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 20; i++ {
		if _, err := w.Write([]byte(strings.Repeat("x", 100) + "\n")); err != nil {
			t.Fatal(err)
		}
	}

	files := archived(t, dir)
	if len(files) != 1 {
		t.Fatalf("同小时内应只有 1 个日志文件，实际 %d 个: %v", len(files), files)
	}
}

// 跨小时轮转：模拟 hour 落后当前一小时，一次写入应触发轮转，
// 旧小时文件保留、新小时文件创建，共 2 个文件
func TestRotateWriter_RotatesByHour(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotateWriter(dir, retention{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// 把活动小时人为置为上一小时，模拟"跨小时"边界
	prevHour := time.Now().Add(-time.Hour).Format("2006010215")
	w.hour = prevHour
	// 旧小时文件需要真实存在并被写入过
	oldPath := filepath.Join(dir, currentLogName(prevHour))
	if err := os.WriteFile(oldPath, []byte("old hour data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.f.Close()
	w.f = nil

	if _, err := w.Write([]byte("new hour data\n")); err != nil {
		t.Fatal(err)
	}

	files := archived(t, dir)
	if len(files) != 2 {
		t.Fatalf("跨小时应保留旧文件并新建 1 个，共 2 个，实际 %d 个: %v", len(files), files)
	}
	// 当前活动文件必须是新小时
	active := filepath.Join(dir, currentLogName(w.hour))
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("新小时活动文件应存在: %v", err)
	}
}

// 保留策略：max_backups 限制历史文件数（不含当前活动文件）
func TestRotateWriter_TrimsToMaxBackups(t *testing.T) {
	dir := t.TempDir()

	// 预先造 4 个旧小时文件（均早于当前小时）
	nowHour := time.Now().Format("2006010215")
	for i := 1; i <= 4; i++ {
		h := time.Now().Add(-time.Duration(i) * time.Hour).Format("2006010215")
		p := filepath.Join(dir, currentLogName(h))
		if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = nowHour
	}

	w, err := newRotateWriter(dir, retention{maxBackups: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// 触发一次轮转（把活动小时置为上一小时）
	prev := time.Now().Add(-time.Hour).Format("2006010215")
	w.hour = prev
	oldPath := filepath.Join(dir, currentLogName(prev))
	_ = os.WriteFile(oldPath, []byte("x\n"), 0o644)
	w.f.Close()
	w.f = nil
	if _, err := w.Write([]byte("trigger\n")); err != nil {
		t.Fatal(err)
	}

	// 期望：当前活动文件 1 个 + 保留的历史文件最多 2 个
	files := archived(t, dir)
	historyCount := 0
	for _, f := range files {
		if f != filepath.Join(dir, currentLogName(w.hour)) {
			historyCount++
		}
	}
	if historyCount > 2 {
		t.Fatalf("历史文件应裁剪到 maxBackups=2，实际 %d 个: %v", historyCount, files)
	}
}

// 保留策略：超过 max_age 的小时文件应被删除，但当前活动文件永不删
func TestRotateWriter_DropsExpiredByAge(t *testing.T) {
	dir := t.TempDir()

	stale := filepath.Join(dir, currentLogName("2020010100"))
	if err := os.WriteFile(stale, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stale, time.Now().Add(-72*time.Hour), time.Now().Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}

	w, err := newRotateWriter(dir, retention{maxAge: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// 触发一次轮转以执行清理
	prev := time.Now().Add(-time.Hour).Format("2006010215")
	w.hour = prev
	oldPath := filepath.Join(dir, currentLogName(prev))
	_ = os.WriteFile(oldPath, []byte("x\n"), 0o644)
	w.f.Close()
	w.f = nil
	if _, err := w.Write([]byte("trigger\n")); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("超过 maxAge 的历史文件应被删除，err=%v", err)
	}
	// 当前活动文件必须还在
	if _, err := os.Stat(filepath.Join(dir, currentLogName(w.hour))); err != nil {
		t.Fatalf("当前活动小时文件不应被删除: %v", err)
	}
}
