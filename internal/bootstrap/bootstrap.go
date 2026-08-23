// Package bootstrap 集中所有进程级依赖的初始化与释放。
//
// 借鉴 agent-proxy 的 bootstrap.MustInit：main 不该知道「有几个组件、按什么顺序起」，
// 它只需要「初始化 -> 启动 -> 等退出」三步。装配细节全部收敛到本包，
// 于是 main.go 读起来就是一张流程图，新增一个依赖也只改这里一处。
//
// 与 agent-proxy 的区别：它把依赖赋给 library/resource 的全局变量，
// 业务层直接 import 取用；这里仍保留 App 结构体与构造注入 ——
// 全局变量会让 service/repository 失去 mock 点，测试只能连真实 DB。
// 「基础设施集中初始化」与「业务层构造注入」这两件事并不冲突。
package bootstrap

import (
	"context"
	"time"

	"myproject/internal/config"
	"myproject/internal/module"
	"myproject/internal/router"
	"myproject/pkg/admin"
	"myproject/pkg/auth"
	"myproject/pkg/cache"
	"myproject/pkg/database"
	"myproject/pkg/health"
	"myproject/pkg/logger"
	"myproject/pkg/metrics"
	"myproject/pkg/transaction"

	"net/http"

	"gorm.io/gorm"
)

// App 持有进程生命周期内的共享资源。
type App struct {
	Cfg      *config.Config
	DB       *gorm.DB
	Redis    *cache.RedisClient
	Health   *health.Registry
	Srv      *http.Server
	AdminSrv *http.Server
}

// Init 按依赖顺序装配组件。任一强依赖失败即返回错误，由 main 决定退出码。
//
// 失败路径也会释放已经建立的资源：中途失败时 app 被丢弃，
// 调用方的 defer app.Close() 还没来得及注册，
// 已连上的 MySQL/Redis 就没人关了（进程退出时靠 OS 兜底，
// 但在测试或未来复用 Init 的场景里就是真泄漏）。
func Init(cfg *config.Config) (*App, error) {
	app := &App{
		Cfg: cfg,
		// 探测结果缓存 2 秒：/readyz 无认证且会真打下游，
		// 不缓存的话外部可以拿它当放大器持续压 DB/Redis。
		Health: health.NewRegistry(2*time.Second, 2*time.Second),
	}
	// 探针里的下游原始错误只在非生产环境返回给调用方（/readyz 无认证）
	app.Health.SetExposeErrors(!cfg.IsProd())

	ok := false
	defer func() {
		if !ok {
			app.Close()
		}
	}()

	// 1. JWT
	jwtManager := auth.NewJWTManager(
		cfg.JWT.Secret,
		time.Duration(cfg.JWT.ExpireTime)*time.Hour,
		cfg.JWT.Issuer,
	)

	// 2. 数据库（强依赖，失败即退出）
	if err := app.initDatabase(); err != nil {
		return nil, err
	}

	// 3. Redis（可通过配置关闭；开启时连接失败即视为配置错误）
	if err := app.initRedis(); err != nil {
		return nil, err
	}

	// 4. HTTP Server。
	// 业务三层不在这里装配：每个模块在 internal/module 下自装配，
	// bootstrap 只提供共享依赖（DB / 事务管理器 / JWT）。
	engine, err := router.Setup(cfg, app.Health, module.Deps{
		DB:  app.DB,
		Tx:  transaction.NewManager(app.DB),
		JWT: jwtManager,
	}, module.All)
	if err != nil {
		return nil, err
	}
	app.Srv = &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           engine,
		ReadTimeout:       cfg.Server.ReadTimeoutDuration(),
		WriteTimeout:      cfg.Server.WriteTimeoutDuration(),
		IdleTimeout:       cfg.Server.IdleTimeoutDuration(),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeoutDuration(),
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
	}

	// 5. admin 服务：/metrics、/debug/pprof、/version。
	// 单独端口且默认只监听回环 —— 这些端点会暴露路由清单、流量特征、堆信息，
	// 不应该挂在业务端口上。
	app.AdminSrv = admin.New(admin.Options{
		Addr:  cfg.Admin.Addr,
		Pprof: cfg.Admin.Pprof,
	})

	ok = true
	return app, nil
}

// DBOptions 把应用配置翻译成 pkg/database 的连接参数。
// 导出是为了让 cmd/migrate 复用同一套翻译，避免两处各写一遍。
func DBOptions(cfg config.DatabaseConfig) database.Options {
	return database.Options{
		Host:            cfg.Host,
		Port:            cfg.Port,
		Username:        cfg.Username,
		Password:        cfg.Password,
		DBName:          cfg.DBName,
		MaxIdleConns:    cfg.MaxIdleConns,
		MaxOpenConns:    cfg.MaxOpenConns,
		ConnMaxLifetime: time.Duration(cfg.ConnMaxLifetime) * time.Minute,
		LogLevel:        cfg.LogLevel,
		SlowThreshold:   time.Duration(cfg.SlowThreshold) * time.Millisecond,
		LogSQLParams:    cfg.LogSQLParams,
	}
}

func (app *App) initDatabase() error {
	cfg := app.Cfg.Database
	db, err := database.NewMySQL(DBOptions(cfg))
	if err != nil {
		return err
	}
	app.DB = db
	logger.Info("database connected", map[string]interface{}{
		"host": cfg.Host, "db": cfg.DBName,
	})

	app.Health.RegisterFunc("database", func(ctx context.Context) error {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.PingContext(ctx)
	})

	// 连接池打满是最常见的「服务变慢但看不出原因」的成因：
	// WaitCount / WaitDuration 持续增长即为信号。
	if sqlDB, err := db.DB(); err == nil {
		if err := metrics.RegisterDBStats("mysql", sqlDB.Stats); err != nil {
			logger.Warn("register db metrics failed", map[string]interface{}{"error": err.Error()})
		}
	}
	return nil
}

func (app *App) initRedis() error {
	cfg := app.Cfg.Redis
	if !cfg.Enabled {
		logger.Warn("redis disabled by config")
		return nil
	}
	redisClient, err := cache.NewRedis(cache.Options{
		Host:     cfg.Host,
		Port:     cfg.Port,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err != nil {
		return err
	}
	app.Redis = redisClient
	logger.Info("redis connected", map[string]interface{}{"host": cfg.Host})
	app.Health.RegisterFunc("redis", redisClient.Ping)
	return nil
}

// Close 释放持有的资源。顺序与初始化相反。
func (app *App) Close() {
	if app.Redis != nil {
		if err := app.Redis.Close(); err != nil {
			logger.Warn("close redis failed", map[string]interface{}{"error": err.Error()})
		} else {
			logger.Info("redis connection closed")
		}
	}
	if app.DB != nil {
		if sqlDB, err := app.DB.DB(); err == nil {
			if err := sqlDB.Close(); err != nil {
				logger.Warn("close database failed", map[string]interface{}{"error": err.Error()})
			} else {
				logger.Info("database connection closed")
			}
		}
	}
}
