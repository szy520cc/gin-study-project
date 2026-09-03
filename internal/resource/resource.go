// Package resource 持有进程级共享资源，业务代码直接取用，不再层层注入。
//
// 为什么改成全局单例：原来每加一个模块都要写 repository/service/controller 三个
// 构造函数、再在 module 包里把它们串起来，四五十行代码没有一行业务逻辑。
// DB、JWT、Redis 这些东西进程内生命周期与进程等长，
// 用「初始化一次 + 全局读取」表达最直接，也是 uniconf 那类项目跑了多年的做法。
//
// 代价是业务层不能再用 mock 替换 DB —— 本项目改走真库集成测试，这个代价已接受。
//
// 多数据源：DB 与 Redis 都以「名字」索引（map）。约定 "default" 为主库，
// DB(ctx) / Redis() 取主库，DBNamed(ctx, name) / RedisNamed(name) 取命名实例。
//
// 约束：只有 bootstrap 和 cmd/* 能调 Set，业务代码只读。
package resource

import (
	"context"
	"fmt"
	"sync"

	"myproject/internal/config"
	"myproject/pkg/auth"
	"myproject/pkg/cache"
	"myproject/pkg/transaction"

	"gorm.io/gorm"
)

var (
	cfg     *config.Config
	dbs     map[string]*gorm.DB
	redises map[string]*cache.RedisClient
	jwt     *auth.JWTManager

	// setOnce 保证资源只装配一次。
	//
	// 用 Once 而不是「谁后写谁生效」：多个变量分别赋值不是原子操作，
	// 并发调用 Set 时另一个 goroutine 可能读到「新 dbs + 旧 jwt」这种半截状态。
	// 启动期本来就只调一次，这里的真正作用是让重复装配变成显式 no-op ——
	// 否则第二次 Set 会把第一次的连接悄悄丢掉，而那个连接再也没人 Close，
	// 连接池和文件句柄一路泄漏到进程退出。
	setOnce sync.Once
)

// Set 由 bootstrap 在启动阶段注入资源，业务代码不要调用。
//
// 只有第一次调用生效：这些资源的生命周期与进程等长，重复装配没有意义，
// 只会让前一个连接失去引用、泄漏出去。map 里没有的数据源即「未启用」，
// 取用时 DBNamed/RedisNamed 会得到 nil 或 panic（见各函数注释）。
func Set(c *config.Config, dbMap map[string]*gorm.DB, redisMap map[string]*cache.RedisClient, jwtManager *auth.JWTManager) {
	setOnce.Do(func() {
		cfg, dbs, redises, jwt = c, dbMap, redisMap, jwtManager
		transaction.Init(dbMap)
	})
}

// DB 返回主库（约定名 "default"）的连接。绝大多数业务用它即可。
func DB(ctx context.Context) *gorm.DB {
	return DBNamed(ctx, config.DefaultDBName)
}

// DBNamed 返回指定数据源的连接。
//
// ctx 中存在事务句柄时复用事务，否则用该数据源的根连接 —— 所以 service 里
// 无论在不在事务中，写法都是 data.Xxx(ctx)，不需要两套函数。
//
// 数据源未装配时 panic 而不是把 nil 传下去：nil *gorm.DB 会在 gorm 的 Session()
// 里解引用空指针，堆栈指向 gorm 内部而不是真正的调用方，「谁取了不存在的库」
// 这条线索直接丢失。这里给出可读信息。「没装配就取用」属于装配错误，用 panic 合适。
//
// 跨库保护：若当前 ctx 已有属于另一个数据源的事务句柄，说明业务在跨库事务里
// 混用了连接 —— 复用 tx 会把别的库的操作写到当前库，属于静默数据错乱。
// 这里直接 panic 拦下，而不是复用错误的 tx。
func DBNamed(ctx context.Context, name string) *gorm.DB {
	db := dbs[name]
	if db == nil {
		panic(fmt.Sprintf("resource: 数据源 %q 未装配，请确认配置了 databases.%s（正常由 bootstrap.Init 完成）", name, name))
	}
	if txName := transaction.CurrentName(ctx); txName != "" {
		if txName != name {
			panic(fmt.Sprintf("resource: 当前事务属于数据源 %q，不能取数据源 %q 的连接（跨库事务不支持）", txName, name))
		}
		if tx, ok := transaction.TxFrom(ctx); ok {
			return tx.WithContext(ctx)
		}
	}
	return db.WithContext(ctx)
}

// RawDBNamed 返回指定数据源的原始连接（不带 ctx），仅供迁移、健康探测这类
// 基础设施使用。业务读写一律走 DB / DBNamed。
func RawDBNamed(name string) *gorm.DB {
	db := dbs[name]
	if db == nil {
		panic(fmt.Sprintf("resource: 数据源 %q 未装配，请确认配置了 databases.%s", name, name))
	}
	return db
}

// JWT 返回 token 签发/校验器
func JWT() *auth.JWTManager { return jwt }

// Redis 返回主 Redis（约定名 "default"），未装配时为 nil
func Redis() *cache.RedisClient { return RedisNamed(config.DefaultDBName) }

// RedisNamed 返回指定 Redis 实例，未装配时为 nil。
// Redis 不是强依赖，返回 nil 由调用方自行判断，而不是像 DB 那样 panic。
func RedisNamed(name string) *cache.RedisClient { return redises[name] }

// Cfg 返回全局配置
func Cfg() *config.Config { return cfg }
