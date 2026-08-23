// Package transaction 提供跨 repository 的事务边界。
//
// 原实现里每个 repository 各自持有 *gorm.DB，service 层拿不到事务句柄，
// 一旦出现「同时写订单和扣库存」这类需求就无法保证原子性。
// 这里把事务句柄放进 context：service 用 Manager.Do 划定边界，
// repository 通过 TxFrom 自动感知，业务代码无需感知 *gorm.DB。
package transaction

import (
	"context"
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
// 置位后 TxFrom 直接返回 false，退化成用默认连接，行为可预期。
type handle struct {
	db   *gorm.DB
	done atomic.Bool
}

// Manager 事务管理器
type Manager interface {
	// Do 在一个事务内执行 fn。fn 返回 error 则整体回滚。
	// 支持嵌套调用：已在事务中时复用外层事务（GORM SavePoint 语义）。
	//
	// fn 收到的 ctx 只在 fn 执行期间有效，不得逃逸到 fn 之外（包括不得传给
	// 后台 goroutine 后延迟使用）—— Do 返回时其中的事务句柄会被置为失效。
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type gormManager struct {
	db *gorm.DB
}

// NewManager 创建基于 GORM 的事务管理器
func NewManager(db *gorm.DB) Manager {
	return &gormManager{db: db}
}

func (m *gormManager) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	// 已经在事务里就复用外层事务句柄。
	//
	// 不这么做的话，嵌套调用会从根连接另起一个独立事务：
	// 外层回滚回滚不掉内层已提交的数据（原子性被破坏），
	// 而且内层占用第二条连接 —— MaxOpenConns 打满时会自己等自己，形成死锁。
	// GORM 在已有事务上调 Transaction 会自动降级为 SavePoint。
	//
	// 嵌套分支同样要 WithContext(ctx)：不带的话内层 SQL 用的是外层 Begin 时的
	// ctx，调用方新收紧的 deadline / 取消信号对内层完全无效。
	// WithContext 只换 Session 的 ctx，ConnPool 仍是外层事务，SavePoint 语义不变。
	if tx, ok := TxFrom(ctx); ok {
		return m.run(ctx, tx.WithContext(ctx), fn)
	}

	return m.run(ctx, m.db.WithContext(ctx), fn)
}

func (m *gormManager) run(ctx context.Context, db *gorm.DB, fn func(ctx context.Context) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		h := &handle{db: tx}
		defer h.done.Store(true)
		return fn(context.WithValue(ctx, ctxKey{}, h))
	})
}

// TxFrom 从 ctx 取事务句柄，供 repository 层判断是否复用事务。
//
// 没有对应的导出 WithTx：写入口一旦导出，任何代码都能把一个普通 *gorm.DB
// 冒充成事务句柄塞进 ctx —— repository 会当事务用（实际每条语句自动提交），
// 嵌套 Do 还会因为它不是 TxCommitter 而另起独立事务。
// 事务的唯一入口是 Manager.Do。
func TxFrom(ctx context.Context) (*gorm.DB, bool) {
	h, ok := ctx.Value(ctxKey{}).(*handle)
	if !ok || h == nil || h.db == nil || h.done.Load() {
		return nil, false
	}
	return h.db, true
}
