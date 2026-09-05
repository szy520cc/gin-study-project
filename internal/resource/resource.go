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
	"sync"

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

	// setOnce 保证资源只装配一次。
	//
	// 用 Once 而不是「谁后写谁生效」：多个变量分别赋值不是原子操作，
	// 并发调用 Set 时另一个 goroutine 可能读到「新 db + 旧 jwt」这种半截状态。
	// 启动期本来就只调一次，这里的真正作用是让重复装配变成显式 no-op ——
	// 否则第二次 Set 会把第一次的连接悄悄丢掉，而那个连接再也没人 Close，
	// 连接池和文件句柄一路泄漏到进程退出。
	setOnce sync.Once
)

// Set 由 bootstrap 在启动阶段注入资源，业务代码不要调用。
// 传 nil 表示该资源未启用（如 Redis 关闭时）。
//
// 只有第一次调用生效：这些资源的生命周期与进程等长，重复装配没有意义。
func Set(c *config.Config, gormDB *gorm.DB, redisClient *cache.RedisClient, jwtManager *auth.JWTManager) {
	setOnce.Do(func() {
		cfg, db, redis, jwt = c, gormDB, redisClient, jwtManager
		transaction.Init(gormDB)
	})
}

// DB 返回本次操作应使用的连接。
//
// ctx 中存在事务句柄时复用事务，否则用默认连接 —— 所以 service 里
// 无论在不在事务中，写法都是 resource.DB(ctx)，不需要两套函数。
//
// 未装配时 panic 而不是把 nil 传下去：nil *gorm.DB 会在 gorm 的 Session()
// 里解引用空指针，堆栈指向 gorm 内部而不是真正的调用方，「谁在装配前就取用了」
// 这条线索直接丢失。「没装配就取用」属于装配错误，用 panic 给出可读信息合适。
func DB(ctx context.Context) *gorm.DB {
	if tx, ok := transaction.TxFrom(ctx); ok {
		return tx.WithContext(ctx)
	}
	mustDB()
	return db.WithContext(ctx)
}

func mustDB() {
	if db == nil {
		panic("resource: 数据库未装配（正常由 bootstrap.Init 调用 resource.Set 完成）")
	}
}

// JWT 返回 token 签发/校验器
func JWT() *auth.JWTManager { return jwt }

// Redis 返回 Redis 客户端，未启用时为 nil
func Redis() *cache.RedisClient { return redis }

// Cfg 返回全局配置
func Cfg() *config.Config { return cfg }
