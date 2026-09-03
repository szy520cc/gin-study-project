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

// TestDoOn_NilDBReturnsError roots 里存了 nil 数据源时，DoOn 必须返回错误而不是 panic。
//
// 这锁住的是「interface 里装 nil 指针」的陷阱：直接 `v == nil` 只判断 interface
// 本身，对 (*gorm.DB)(nil) 会漏判，随后 .WithContext 就空指针 panic。
func TestDoOn_NilDBReturnsError(t *testing.T) {
	roots.Store("nil_db", (*gorm.DB)(nil))
	defer roots.Delete("nil_db")

	err := DoOn(context.Background(), "nil_db", func(ctx context.Context) error { return nil })
	if err == nil {
		t.Error("nil 数据源应返回错误，而不是 panic 或静默通过")
	}
}

// TestCurrentName 锁住「事务属于哪个数据源」的识别。
// resource.DBNamed 靠它拒绝跨库混用连接，这里验证无事务时为空、有事务时返回所属名。
func TestCurrentName(t *testing.T) {
	if n := CurrentName(context.Background()); n != "" {
		t.Errorf("无事务时 CurrentName 应为空，实际 %q", n)
	}

	h := &handle{db: &gorm.DB{}, name: "analytics"}
	ctx := context.WithValue(context.Background(), ctxKey{}, h)
	if n := CurrentName(ctx); n != "analytics" {
		t.Errorf("CurrentName 应返回 %q，实际 %q", "analytics", n)
	}
}
