// Package resource 持有进程级共享资源，业务代码直接取用，不再层层注入。
//
// 为什么改成全局单例：原来每加一个模块都要写 repository/service/controller 三个
// 构造函数、再在 module 包里把它们串起来，四五十行代码没有一行业务逻辑。
// DB、JWT、Redis 这些东西进程内只有一份、生命周期与进程等长，
// 用「初始化一次 + 全局读取」表达最直接，也是 uniconf 那类项目跑了多年的做法。
//
// 代价是业务层不能再用 mock 替换 DB —— 本项目改走真库集成测试，这个代价已接受。
//
// 约束：只有 bootstrap 和 cmd/* 能调 Init/Close，业务代码只读。
package resource

import (
	"context"

	"myproject/internal/config"
	"myproject/pkg/auth"
	"myproject/pkg/cache"
	"myproject/pkg/transaction"

	"gorm.io/gorm"
)

var (
	cfg   *config.Config
	db    *gorm.DB
	redis *cache.RedisClient
	jwt   *auth.JWTManager
)

// Set 由 bootstrap 在启动阶段注入资源，业务代码不要调用。
// 传 nil 表示该资源未启用（如 Redis 关闭时）。
func Set(c *config.Config, gormDB *gorm.DB, redisClient *cache.RedisClient, jwtManager *auth.JWTManager) {
	cfg, db, redis, jwt = c, gormDB, redisClient, jwtManager
	transaction.Init(gormDB)
}

// DB 返回本次操作应使用的连接。
//
// ctx 中存在事务句柄时复用事务，否则用默认连接 —— 所以 service 里
// 无论在不在事务中，写法都是 resource.DB(ctx)，不需要两套函数。
func DB(ctx context.Context) *gorm.DB {
	if tx, ok := transaction.TxFrom(ctx); ok {
		return tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}

// RawDB 返回不带 ctx 的原始连接，仅供迁移、健康探测这类基础设施使用
func RawDB() *gorm.DB { return db }

// JWT 返回 token 签发/校验器
func JWT() *auth.JWTManager { return jwt }

// Redis 返回 Redis 客户端，未启用时为 nil
func Redis() *cache.RedisClient { return redis }

// Cfg 返回全局配置
func Cfg() *config.Config { return cfg }
