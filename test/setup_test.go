// Package test 是真库集成测试：起真实的 gin 引擎、连真实的 MySQL，
// 通过 HTTP 请求验证整条链路。
//
// 不用 mock/stub：框架里已经没有 interface 可以替换实现了，
// 而「service 直接写 gorm」这种代码只有连真库才能验出 SQL 是否正确。
//
// 数据库连不上时全部跳过（不是失败）：本地没起库、CI 没配库的情况下
// 仍然能跑 go test ./...，但会明确打印跳过原因，不会静默变成绿灯。
package test

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"myproject/internal/bootstrap"
	"myproject/internal/config"
	"myproject/internal/model"
	"myproject/internal/resource"
	"myproject/internal/router"
	"myproject/pkg/auth"
	"myproject/pkg/database"
	"myproject/pkg/health"
	"myproject/pkg/logger"

	"gorm.io/gorm"
)

var (
	testEngine http.Handler
	testDB     *gorm.DB
	dbSkip     string
)

// TestMain 全局初始化：连库 → 建表 → 注入 resource → 构建引擎。
// 所有用例共用这一份，避免每个用例重复连库。
//
// 数据库连不上时仍然会把引擎建起来：路由表、中间件、参数校验这些
// 框架层行为不碰 DB，照样可以验证；只有真正读写数据的用例才跳过。
func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		fmt.Fprintf(os.Stderr, "测试初始化失败: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if testDB != nil {
		if sqlDB, err := testDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	os.Exit(code)
}

func setup() error {
	if err := logger.Init(logger.Options{Level: "error", Format: "console"}); err != nil {
		return err
	}

	// 配置来源与线上一致：configs/ 下的 yaml + APP_* 环境变量覆盖。
	// 测试固定用 dev 环境，绝不会连到 prod 配置。
	// jwt.secret 只能从环境变量来（配置校验强制），测试里补一个假值。
	if os.Getenv("APP_JWT_SECRET") == "" {
		if err := os.Setenv("APP_JWT_SECRET", "test-only-secret-0123456789abcdef"); err != nil {
			return err
		}
	}
	cfg, err := config.Load(configDir(), "dev")
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	cfg.Server.Mode = "test"
	cfg.Log.LogBody = false
	// 共享引擎关掉限流：集成测试会连续发几十个请求，
	// 否则用例之间会互相把配额吃光。限流本身由 TestProbesBypassRateLimit
	// 这类用例自己建引擎单独验证。
	cfg.RateLimit.Enabled = false

	// 连库失败不终止：记下原因，读写类用例跳过
	db, err := database.NewMySQL(bootstrap.DBOptions(cfg.Database))
	switch {
	case err != nil:
		dbSkip = fmt.Sprintf("连接数据库失败（%s:%d/%s）: %v",
			cfg.Database.Host, cfg.Database.Port, cfg.Database.DBName, err)
	default:
		if err := db.AutoMigrate(&model.User{}, &model.Project{}, &model.Field{}, &model.ConfigPack{}, &model.Config{}, &model.Rule{}); err != nil {
			dbSkip = fmt.Sprintf("建表失败: %v", err)
		} else {
			testDB = db
		}
	}

	health.Init(time.Second, 0)
	resource.Set(cfg, db, nil, auth.NewJWTManager(cfg.JWT.Secret, time.Hour, "myproject-test"))

	engine, err := router.Setup(cfg)
	if err != nil {
		return fmt.Errorf("构建路由失败: %w", err)
	}
	testEngine = engine
	return nil
}

// configDir 测试从 test/ 目录运行，配置在上一层
func configDir() string {
	if v := os.Getenv("APP_CONFIG_DIR"); v != "" {
		return v
	}
	return "../configs"
}

// requireDB 数据库不可用时跳过用例。
// 跳过时必须打印原因：否则「没连上库」会伪装成一片绿灯。
func requireDB(t *testing.T) {
	t.Helper()
	if testDB == nil {
		t.Skipf("跳过真库用例: %s（设置 APP_DATABASE_HOST/USERNAME/PASSWORD/DBNAME 或 make docker-up 后重跑）", dbSkip)
	}
}
