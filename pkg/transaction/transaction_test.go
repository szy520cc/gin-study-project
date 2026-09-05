package transaction

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

// TestTxFrom_InvalidAfterDone 事务结束后 ctx 里的句柄必须失效。
//
// 白盒测试：Do 的完整流程需要真实数据库，这里只锁住「失效」这一条语义 ——
// 闭包若把 ctx 交给后台 goroutine（safego.Go 就是这个签名）或存进结构体，
// Do 返回后继续用它写库会撞上 sql.ErrTxDone，或与 Commit 并发使用同一个 *sql.Tx。
func TestTxFrom_InvalidAfterDone(t *testing.T) {
	h := &handle{db: &gorm.DB{}}
	ctx := context.WithValue(context.Background(), ctxKey{}, h)

	if _, ok := TxFrom(ctx); !ok {
		t.Fatal("事务进行中应能取到句柄")
	}

	h.done.Store(true)
	if _, ok := TxFrom(ctx); ok {
		t.Error("事务结束后不应再取到句柄")
	}
}

// TestTxFrom_EmptyContext 没有事务的 ctx 不应误判为事务中
func TestTxFrom_EmptyContext(t *testing.T) {
	if _, ok := TxFrom(context.Background()); ok {
		t.Error("空 ctx 不应取到事务句柄")
	}
	if _, ok := TxFrom(context.WithValue(context.Background(), ctxKey{}, (*handle)(nil))); ok {
		t.Error("nil 句柄不应判为事务中")
	}
}

// TestDo_RootNotInitialized 根连接未装配时必须返回可读错误，而不是让 nil
// 流进 gorm 的 Session() 里解引用空指针 —— 那样堆栈落在 gorm 内部，
// 看不出是「谁忘了调 bootstrap.Init」。
//
// 本用例会临时清空包级 root，跑完恢复；因为要独占这个全局状态，不能并行。
func TestDo_RootNotInitialized(t *testing.T) {
	saved := root.Load()
	root.Store(nil)
	t.Cleanup(func() { root.Store(saved) })

	called := false
	err := Do(context.Background(), func(ctx context.Context) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("根连接未装配时应返回 error")
	}
	if called {
		t.Error("取不到连接时闭包不该被执行")
	}
}
