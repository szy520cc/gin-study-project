// Package bootstrap 集中所有进程级依赖的初始化、启动与释放。
//
// main 不该知道「有几个组件、按什么顺序起」，它只需要「初始化 → 启动 → 等退出」。
// 装配细节全部收敛在这里：连 DB、连 Redis、签发器、探针注册、HTTP Server，
// 以及启动两个端口、收信号、摘流、优雅关闭。
// 建好之后统一交给 internal/resource 持有，业务层直接取用。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"myproject/internal/config"
	"myproject/internal/resource"
	"myproject/internal/router"
	"myproject/pkg/admin"
	"myproject/pkg/auth"
	"myproject/pkg/buildinfo"
	"myproject/pkg/cache"
	"myproject/pkg/database"
	"myproject/pkg/health"
	"myproject/pkg/logger"
	"myproject/pkg/metrics"
	"myproject/pkg/safego"

	"gorm.io/gorm"
)

// App 持有进程生命周期内需要显式关闭的资源。
type App struct {
	Cfg      *config.Config
	DB       *gorm.DB
	Redis    *cache.RedisClient
	Srv      *http.Server
	AdminSrv *http.Server
}

// Init 按依赖顺序装配组件。任一强依赖失败即返回错误，由 main 决定退出码。
//
// 失败路径也会释放已经建立的资源：中途失败时 app 被丢弃，
// 调用方的 defer app.Close() 还没来得及注册，已连上的 MySQL/Redis 就没人关了。
func Init(cfg *config.Config) (*App, error) {
	app := &App{Cfg: cfg}

	// 探测结果缓存 2 秒：/readyz 无认证且会真打下游，
	// 不缓存的话外部可以拿它当放大器持续压 DB/Redis。
	health.Init(2*time.Second, 2*time.Second)
	// 探针里的下游原始错误只在非生产环境返回给调用方（/readyz 无认证）
	health.SetExposeErrors(!cfg.IsProd())

	ok := false
	defer func() {
		if !ok {
			app.Close()
		}
	}()

	// 1. 数据库（强依赖，失败即退出）
	if err := app.initDatabase(); err != nil {
		return nil, err
	}

	// 2. Redis（可通过配置关闭；开启时连接失败即视为配置错误）
	if err := app.initRedis(); err != nil {
		return nil, err
	}

	// 3. 资源交给全局单例，此后业务层可以用 resource.DB(ctx) / resource.JWT()
	resource.Set(cfg, app.DB, app.Redis, auth.NewJWTManager(
		cfg.JWT.Secret,
		time.Duration(cfg.JWT.ExpireTime)*time.Hour,
		cfg.JWT.Issuer,
	))

	// 4. HTTP Server
	engine, err := router.Setup(cfg)
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

	health.RegisterFunc("database", func(ctx context.Context) error {
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
	health.RegisterFunc("redis", redisClient.Ping)
	return nil
}

// ---------- 启动与退出 ----------

// Run 启动业务与 admin 两个 server，等待退出信号，再摘流并优雅关闭。
//
// 「起几个 server、谁失败要退出、谁失败只告警」这类判断集中在这一个函数里，
// main 不必感知。
func (app *App) Run() error {
	// NotifyContext 比手工 signal.Notify 更简洁，且能把取消信号传给下游。
	// 收到信号后会显式调用 stop() 并改为监听「第二次信号」，
	// 这里的 defer 只兜住从 srvErr 分支直接返回的路径。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srvErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", map[string]interface{}{
			"addr":    app.Cfg.Server.Addr,
			"mode":    app.Cfg.Server.Mode,
			"version": buildinfo.Version,
			"go":      buildinfo.GoVersion(),
		})
		if err := app.Srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
	}()

	// admin 端口起不来不影响业务：只告警，不退出
	if app.AdminSrv != nil {
		safego.Go(context.Background(), "admin-server", func() {
			logger.Info("admin server starting", map[string]interface{}{
				"addr":  app.AdminSrv.Addr,
				"pprof": app.Cfg.Admin.Pprof,
			})
			if err := app.AdminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("admin server failed", map[string]interface{}{"error": err.Error()})
			}
		})
	}

	select {
	case err := <-srvErr:
		return fmt.Errorf("服务启动失败: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	// 恢复信号的默认行为，并单独监听第二次信号。
	//
	// NotifyContext 注册的处理器在 Run 返回前一直生效，drain + Shutdown
	// 期间（生产配置合计可达 40 秒）第二个 SIGTERM 会被吞掉、默认行为也被抑制，
	// 运维只剩 SIGKILL 可用。这里给出「再按一次立刻退出」的逃生口。
	stop()
	forced := make(chan os.Signal, 1)
	signal.Notify(forced, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-forced
		logger.Warn("second shutdown signal received, exiting immediately")
		// os.Exit 会跳过 main 的 defer app.Close()，这里先尽量释放 DB/Redis 连接
		app.Close()
		os.Exit(1)
	}()

	app.drain()

	// 停止接收新连接，等待存量请求处理完
	shutdownCtx, cancel := context.WithTimeout(context.Background(), app.Cfg.Server.ShutdownTimeoutDuration())
	defer cancel()

	if err := admin.Shutdown(shutdownCtx, app.AdminSrv); err != nil {
		logger.Warn("admin server shutdown failed", map[string]interface{}{"error": err.Error()})
	}

	if err := app.Srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", map[string]interface{}{"error": err.Error()})
		return err
	}

	logger.Info("server exited gracefully")
	return nil
}

// drain 摘流：先让 /readyz 返回 503，等负载均衡把本实例从后端列表摘掉，
// 再进入 Shutdown。
//
// 少了这一步，SIGTERM 之后 LB 仍会在下一次探测前继续转发流量，
// 而此时服务已经拒绝新连接 —— 表现为发布期间的一小批 502。
func (app *App) drain() {
	delay := app.Cfg.Server.DrainDelayDuration()
	if delay <= 0 {
		return
	}

	health.StartDraining()
	logger.Info("draining: readiness set to unhealthy", map[string]interface{}{
		"delay": delay.String(),
	})
	time.Sleep(delay)
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
