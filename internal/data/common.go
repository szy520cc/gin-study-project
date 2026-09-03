// Package data 是数据访问层：这一层是唯一允许出现 SQL 与 gorm 调用的地方。
//
// 三条约定：
//
//  1. 全部是包级函数，没有 interface、没有构造函数、没有 struct 字段注入。
//     需要连接就调本包的 connDb(ctx)，它会在事务中自动复用事务句柄
//     （见 pkg/transaction 与 resource.DB），所以同一个函数在事务内外都能用，
//     不需要为事务写第二套 XxxWithTx。
//
//  2. 不认识业务错误。本层只返回 gorm 原始错误，由 service 用 IsNotFound /
//     IsDuplicate 判定后翻译成 errcode。多一层 ErrNotFound/ErrConflict 哨兵
//     等于让同一个错误翻译两次，而两层共用一个哨兵曾经真的导致过误判。
//
//  3. 不做业务判断。归属校验、状态流转、分页上限都属于 service；
//     本层只负责「按给定条件读写数据」。
package data

import (
	"context"
	"errors"

	"myproject/internal/resource"

	"gorm.io/gorm"
)

// connDb 返回本次操作应使用的连接（主库）。
// ctx 中存在事务句柄时复用事务，否则用根连接；两条路径都已 WithContext(ctx)。
func connDb(ctx context.Context) *gorm.DB {
	return resource.DB(ctx)
}

// connNamedDb 返回指定数据源的连接，供需要读写非主库的数据函数使用。
// 主库用 connDb 即可；命名库只在确有多个库时才需要。
func connNamedDb(ctx context.Context, name string) *gorm.DB {
	return resource.DBNamed(ctx, name)
}

// IsNotFound 记录不存在
func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }

// IsDuplicate 唯一索引冲突。
// gorm 的 TranslateError 已开启（见 pkg/database），所以能直接判 ErrDuplicatedKey。
func IsDuplicate(err error) bool { return errors.Is(err, gorm.ErrDuplicatedKey) }
