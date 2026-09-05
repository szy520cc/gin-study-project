// Package transaction 提供事务边界。
//
// service 用 transaction.Do 划定边界，resource.DB(ctx) 会自动认领 ctx 里的
// 事务句柄，所以业务代码不需要为「在事务里」和「不在事务里」写两套函数。
package transaction

import (
	"context"
	"fmt"
	"sync/atomic"

	"gorm.io/gorm"
)

type ctxKey struct{}

// handle 包装事务句柄，附带一个「事务已结束」标志。
//
// 不直接把 *gorm.DB 放进 ctx，是为了让句柄能在 Do 返回后失效：
// 闭包里如果把 ctx 交给后台 goroutine（safego.Go 就是这个签名）或存进结构体，
// 事务早已 Commit/Rollback，继续拿它写库会撞上 sql.ErrTxDone，
// 更糟的情况是与 Commit 并发使用同一个 *sql.Tx（database/sql 不保证安全）。
type handle struct {
	db   *gorm.DB
	done atomic.Bool
}

var root atomic.Pointer[gorm.DB]

// Init 由 bootstrap 注入根连接，业务代码不要调用
func Init(db *gorm.DB) { root.Store(db) }

// Do 在一个事务内执行 fn。fn 返回 error 则整体回滚。
//
// 支持嵌套调用：已在事务中时复用外层事务（GORM SavePoint 语义），
// 内层失败只回滚到内层的 savepoint，外层仍可提交。
//
// fn 收到的 ctx 只在 fn 执行期间有效，不得逃逸到 fn 之外（包括不得传给
// 后台 goroutine 后延迟使用）—— Do 返回时其中的事务句柄会被置为失效。
func Do(ctx context.Context, fn func(ctx context.Context) error) error {
	// 嵌套分支要 WithContext(ctx)：不带的话内层 SQL 用的是外层 Begin 时的 ctx，
	// 调用方新收紧的 deadline / 取消信号对内层完全无效。
	// WithContext 只换 Session 的 ctx，ConnPool 仍是外层事务，SavePoint 语义不变。
	if tx, ok := TxFrom(ctx); ok {
		return run(ctx, tx.WithContext(ctx), fn)
	}

	// 根连接未装配时返回错误而不是让 nil 流下去：nil *gorm.DB 会在 gorm 的
	// Session() 里解引用空指针，堆栈落在 gorm 内部，看不出是谁忘了装配。
	db := root.Load()
	if db == nil {
		return fmt.Errorf("transaction: 根连接未装配（正常由 bootstrap.Init 完成）")
	}
	return run(ctx, db.WithContext(ctx), fn)
}

func run(ctx context.Context, db *gorm.DB, fn func(ctx context.Context) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		h := &handle{db: tx}
		defer h.done.Store(true)
		return fn(context.WithValue(ctx, ctxKey{}, h))
	})
}

// TxFrom 从 ctx 取事务句柄，供 resource.DB 判断是否复用事务。
//
// 没有对应的导出 WithTx：写入口一旦导出，任何代码都能把一个普通 *gorm.DB
// 冒充成事务句柄塞进 ctx —— 那样看起来在事务里，实际每条语句自动提交。
// 事务的唯一入口是 Do。
func TxFrom(ctx context.Context) (*gorm.DB, bool) {
	h, ok := ctx.Value(ctxKey{}).(*handle)
	if !ok || h == nil || h.db == nil || h.done.Load() {
		return nil, false
	}
	return h.db, true
}
