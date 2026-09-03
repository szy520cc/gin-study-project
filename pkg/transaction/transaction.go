// Package transaction 提供事务边界。
//
// service 用 transaction.Do 划定边界，resource.DB(ctx) 会自动认领 ctx 里的
// 事务句柄，所以业务代码不需要为「在事务里」和「不在事务里」写两套函数。
//
// 多数据源：Do 在主库（DefaultName）开事务，DoOn 在指定数据源开事务。
// 嵌套只允许同库复用（SavePoint 语义），跨库嵌套会报错 —— 跨库事务
// 需要分布式事务协调，不在本包职责内。
//
// 为什么不直接用 gorm 的 db.Transaction：
//
//  1. 本项目的 data 层是包级函数，签名统一为 Xxx(ctx, ...)，连接靠
//     resource.DB(ctx) 取。若改用原生事务，必须把 *gorm.DB 显式传进每一个
//     data 函数（全部加 tx 参数，且 service 要接触 *gorm.DB），否则 data 层
//     拿到的仍是根连接，压根不在事务里 —— 等于退回「为事务写第二套函数」。
//
//  2. 原生事务的 tx 逃出闭包后仍可被调用，但事务早已 Commit/Rollback，继续用
//     会撞 sql.ErrTxDone；本包用 done 标志让句柄在 Do 返回后失效（见 handle）。
//
//  3. 嵌套调用时，原生事务要调用方自己判断「是否已在事务中」并降级 SavePoint；
//     本包在 Do 里统一处理。
package transaction

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"gorm.io/gorm"
)

// DefaultName 默认数据源名，与 internal/config.DefaultDBName 保持一致。
// pkg 层不能依赖 internal，所以这里重复声明这个字符串字面量。
const DefaultName = "default"

type ctxKey struct{}

// handle 包装事务句柄，附带「事务已结束」标志与所属数据源名。
//
// 不直接把 *gorm.DB 放进 ctx，是为了让句柄能在 Do 返回后失效：
// 闭包里如果把 ctx 交给后台 goroutine（safego.Go 就是这个签名）或存进结构体，
// 事务早已 Commit/Rollback，继续拿它写库会撞上 sql.ErrTxDone，
// 更糟的情况是与 Commit 并发使用同一个 *sql.Tx（database/sql 不保证安全）。
// name 用于拒绝跨库嵌套：内层 DoOn 指定了另一个库时，复用外层句柄会把
// 别的库的操作写到当前库上，属于静默的数据错乱。
type handle struct {
	db   *gorm.DB
	name string
	done atomic.Bool
}

// roots 持有每个数据源的根连接。Init 只在启动期写一次，此后只读，
// 用 sync.Map 兜住并发读的边界。
var roots sync.Map

// Init 由 bootstrap 注入各数据源根连接，业务代码不要调用。
func Init(dbs map[string]*gorm.DB) {
	for name, db := range dbs {
		roots.Store(name, db)
	}
}

// Do 在主库（DefaultName）内执行 fn，fn 返回 error 则整体回滚。
func Do(ctx context.Context, fn func(ctx context.Context) error) error {
	return DoOn(ctx, DefaultName, fn)
}

// DoOn 在指定数据源内执行 fn。
//
// 支持嵌套调用：已在同一数据源的事务中时复用外层事务（GORM SavePoint 语义），
// 内层失败只回滚到内层的 savepoint，外层仍可提交。嵌套到另一个数据源会直接报错。
//
// fn 收到的 ctx 只在 fn 执行期间有效，不得逃逸到 fn 之外（包括不得传给
// 后台 goroutine 后延迟使用）—— DoOn 返回时其中的事务句柄会被置为失效。
func DoOn(ctx context.Context, name string, fn func(ctx context.Context) error) error {
	// 嵌套分支要 WithContext(ctx)：不带的话内层 SQL 用的是外层 Begin 时的 ctx，
	// 调用方新收紧的 deadline / 取消信号对内层完全无效。
	// WithContext 只换 Session 的 ctx，ConnPool 仍是外层事务，SavePoint 语义不变。
	if h, ok := currentHandle(ctx); ok {
		if h.name != name {
			return fmt.Errorf("transaction: 已在数据源 %q 的事务中，不能嵌套到 %q（跨库事务需在外层协调）", h.name, name)
		}
		return run(ctx, name, h.db.WithContext(ctx), fn)
	}

	// 根连接未装配时返回错误而不是让 nil 流下去：nil *gorm.DB 会在 gorm 的
	// Session() 里解引用空指针，堆栈落在 gorm 内部，看不出是谁忘了装配。
	//
	// 这里必须用类型断言再判 nil 指针：sync.Map 里存的可能是 (*gorm.DB)(nil)，
	// 直接 `v == nil` 只判断 interface 是否为 nil，对「装了 nil 指针」的条目
	// 会漏判，接着 v.(*gorm.DB).WithContext(ctx) 就 panic。
	v, _ := roots.Load(name)
	db, ok := v.(*gorm.DB)
	if !ok || db == nil {
		return fmt.Errorf("transaction: 数据源 %q 未装配，请确认配置了 databases.%s", name, name)
	}
	return run(ctx, name, db.WithContext(ctx), fn)
}

func run(ctx context.Context, name string, db *gorm.DB, fn func(ctx context.Context) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		h := &handle{db: tx, name: name}
		defer h.done.Store(true)
		return fn(context.WithValue(ctx, ctxKey{}, h))
	})
}

// currentHandle 取当前事务句柄，已结束或不存在时返回 false。
func currentHandle(ctx context.Context) (*handle, bool) {
	h, ok := ctx.Value(ctxKey{}).(*handle)
	if !ok || h == nil || h.db == nil || h.done.Load() {
		return nil, false
	}
	return h, true
}

// TxFrom 从 ctx 取事务句柄，供 resource.DBNamed 判断是否复用事务。
//
// 没有对应的导出 WithTx：写入口一旦导出，任何代码都能把一个普通 *gorm.DB
// 冒充成事务句柄塞进 ctx —— 那样看起来在事务里，实际每条语句自动提交。
// 事务的唯一入口是 Do / DoOn。
func TxFrom(ctx context.Context) (*gorm.DB, bool) {
	h, ok := currentHandle(ctx)
	if !ok {
		return nil, false
	}
	return h.db, true
}

// CurrentName 返回当前 ctx 中事务所属的数据源名，无事务时返回空串。
//
// 供 resource.DBNamed 判断「要取的数据源」和「当前事务的数据源」是否一致：
// 不一致说明业务在跨库事务里混用了连接 —— 复用 tx 会把别的库的操作写到当前库，
// 属于静默的数据错乱，必须在取连接这一层就拦住。
func CurrentName(ctx context.Context) string {
	h, ok := currentHandle(ctx)
	if !ok {
		return ""
	}
	return h.name
}
