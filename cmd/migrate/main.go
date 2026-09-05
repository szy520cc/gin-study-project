// Command migrate 执行数据库结构迁移。
//
// Makefile 里的 `make migrate` 原本指向这个不存在的文件，执行必然失败。
// 这里用 GORM AutoMigrate 提供最小可用实现：不引入新依赖，
// 适合开发期同步表结构。
//
// 生产环境建议换成 golang-migrate/atlas 这类带版本号和回滚能力的方案：
// AutoMigrate 不会删除字段、不生成变更记录，无法审计与回滚。
package main

import (
	"flag"
	"fmt"
	"os"

	"myproject/internal/bootstrap"
	"myproject/internal/config"
	"myproject/internal/model"
	"myproject/pkg/database"
	"myproject/pkg/logger"
)

func main() {
	env := flag.String("env", envOrDefault("APP_ENV", "dev"), "运行环境 (dev, test, prod)")
	configPath := flag.String("config", "./configs", "配置文件目录")
	drop := flag.Bool("drop", false, "危险：迁移前删除已有表（仅限本地开发）")
	confirm := flag.String("confirm", "", "配合 -drop：必须填写目标库名，与实际连接的库不一致即拒绝")
	flag.Parse()

	cfg, err := config.Load(*configPath, *env)
	if err != nil {
		exit(err)
	}

	if err := logger.Init(logger.Options{
		Level:  cfg.Log.Level,
		Format: "console",
	}); err != nil {
		exit(err)
	}
	defer func() { _ = logger.Close() }()

	// -drop 的保护不能只看 env 标签：database.* 全都可以被 APP_DATABASE_* 覆盖，
	// `APP_ENV=dev APP_DATABASE_HOST=<生产库> migrate -drop` 一样能把生产表删光。
	// 所以除了拒绝 prod，还要求显式写出实际连上的库名。
	if *drop {
		if cfg.IsProd() {
			exit(fmt.Errorf("拒绝在生产环境执行 -drop"))
		}
		if *confirm != cfg.Database.DBName {
			exit(fmt.Errorf("-drop 需要 -confirm <库名>：当前目标是 %s@%s:%d/%s，请确认后重试",
				cfg.Database.Username, cfg.Database.Host, cfg.Database.Port, cfg.Database.DBName))
		}
	}

	db, err := database.NewMySQL(bootstrap.DBOptions(cfg.Database))
	if err != nil {
		exit(err)
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	// 新增模型时在这里登记
	models := []interface{}{
		&model.User{},
		&model.Order{},
		&model.OrderStatusLog{},
		&model.Project{},
		&model.Field{},
		&model.ConfigPack{},
		&model.Config{},
	}

	if *drop {
		logger.Warn("dropping tables before migration", map[string]interface{}{
			"host": cfg.Database.Host, "port": cfg.Database.Port, "db": cfg.Database.DBName,
		})
		if err := db.Migrator().DropTable(models...); err != nil {
			exit(err)
		}
	}

	if err := db.AutoMigrate(models...); err != nil {
		exit(err)
	}

	logger.Info("migration completed", map[string]interface{}{
		"env": *env, "db": cfg.Database.DBName, "models": len(models),
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func exit(err error) {
	fmt.Fprintf(os.Stderr, "migrate failed: %v\n", err)
	os.Exit(1)
}
