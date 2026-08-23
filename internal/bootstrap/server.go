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

	"myproject/pkg/admin"
	"myproject/pkg/buildinfo"
	"myproject/pkg/logger"
	"myproject/pkg/safego"
)

// Run 启动业务与 admin 两个 server，等待退出信号，再摘流并优雅关闭。
//
// 对应 agent-proxy 的 servers/start.go：进程可能同时监听多个端口，
// 「起几个 server、谁失败要退出谁失败只告警」这类判断集中在一处，
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

	app.Health.StartDraining()
	logger.Info("draining: readiness set to unhealthy", map[string]interface{}{
		"delay": delay.String(),
	})
	time.Sleep(delay)
}
