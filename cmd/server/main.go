// Command server 是服务的启动入口。
//
// 这里只做四件事：解析配置来源 -> 初始化日志 -> bootstrap.Init 装配依赖 -> Run。
// 组件有哪些、按什么顺序起、怎么关，全部在 internal/bootstrap 里，
// 本文件应当保持在几十行内 —— 一眼能看完启动流程。
package main

import (
	"flag"
	"fmt"
	"os"

	"myproject/internal/bootstrap"
	"myproject/internal/config"
	"myproject/pkg/logger"
	"myproject/pkg/response"
)

func main() {
	// main 只负责决定退出码，真正的流程在 run 里，
	// 这样所有 defer（关日志、关连接）都能正常执行。
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// 加载配置
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// 日志优先初始化：后续所有组件的启动日志都要经过它
	err = logger.Init(logger.Options{
		Level:      cfg.Log.Level,
		Format:     cfg.Log.Format,
		Dir:        cfg.Log.FilePath,
		AddSource:  cfg.Log.AddSource,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAgeDays: cfg.Log.MaxAgeDays,
	})
	if err != nil {
		// 文件不可写不应阻塞启动，降级为仅 stdout
		fmt.Fprintf(os.Stderr, "warn: %v，日志降级为仅 stdout\n", err)
	}
	defer func() {
		_ = logger.Close()
	}()

	// 生产环境不对外暴露错误细节
	response.SetExposeDetails(!cfg.IsProd())

	app, err := bootstrap.Init(cfg)
	if err != nil {
		logger.Error("failed to setup application", map[string]interface{}{"error": err.Error()})
		return err
	}
	defer app.Close()

	return app.Run()
}

// 加载配置文件，优先级：命令行参数 > 环境变量 > 默认值。
func loadConfig() (*config.Config, error) {
	defaultEnv := os.Getenv("APP_ENV")
	if defaultEnv == "" {
		defaultEnv = "dev"
	}

	env := flag.String("env", defaultEnv, "运行环境 (dev, test, prod)")
	configPath := flag.String("config", "./configs", "配置文件目录")
	flag.Parse()

	cfg, err := config.Load(*configPath, *env)
	if err != nil {
		return nil, fmt.Errorf("加载配置失败: %w", err)
	}
	return cfg, nil
}
