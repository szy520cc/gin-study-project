package repository

import (
	"context"
	"errors"
	"fmt"

	"myproject/pkg/transaction"

	"gorm.io/gorm"
)

// 领域层错误：repository 是 ORM 的边界，向上只暴露这些错误，
// 这样 service 层不必 import gorm，将来替换 ORM 也不影响业务代码。
var (
	// ErrNotFound 记录不存在
	ErrNotFound = errors.New("repository: record not found")
	// ErrConflict 唯一键冲突
	ErrConflict = errors.New("repository: unique constraint conflict")
)

// base 所有 repository 的公共基类，负责事务感知与错误转换
type base struct {
	db *gorm.DB
}

func newBase(db *gorm.DB) base {
	return base{db: db}
}

// conn 返回本次操作应使用的连接：
// ctx 中存在事务句柄时复用事务，否则使用默认连接。
// 两条路径都要 WithContext(ctx)：事务句柄携带的是 Begin 时的 ctx，
// 不换的话事务内的查询不受调用方超时与取消约束。
func (b base) conn(ctx context.Context) *gorm.DB {
	if tx, ok := transaction.TxFrom(ctx); ok {
		return tx.WithContext(ctx)
	}
	return b.db.WithContext(ctx)
}

// wrapErr 把 ORM 错误转换成领域错误
func wrapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return ErrNotFound
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return ErrConflict
	default:
		return fmt.Errorf("repository: %w", err)
	}
}
