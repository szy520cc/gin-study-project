// Package safego 提供带 panic 保护的 goroutine 启动方式。
//
// 裸 go func 里的 panic 无法被 gin 的 Recovery 中间件捕获 ——
// 它会直接终止整个进程。任何在请求链路之外启动的 goroutine
// （后台清理、并发探测、异步任务）都应该走这里。
package safego

import (
	"context"
	"fmt"
	"runtime"

	"myproject/pkg/logger"
	"myproject/pkg/metrics"
)

// Go 启动一个带 panic 保护的 goroutine
func Go(ctx context.Context, name string, fn func()) {
	go func() {
		defer recoverPanic(ctx, name)
		fn()
	}()
}

// Run 同步执行并保护 panic，用于已经在 goroutine 内部的场景
func Run(ctx context.Context, name string, fn func()) {
	defer recoverPanic(ctx, name)
	fn()
}

// RunE 同步执行并把 panic 转换成 error 返回。
// 用于「panic 不该终止进程，但调用方需要知道这次失败了」的场景，
// 例如健康检查：探测实现 panic 时必须报告为不健康，而不是静默当成健康。
func RunE(ctx context.Context, name string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logStack(ctx, name, r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn()
}

func recoverPanic(ctx context.Context, name string) {
	if r := recover(); r != nil {
		logStack(ctx, name, r)
	}
}

func logStack(ctx context.Context, name string, r any) {
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	logger.C(ctx).Error("goroutine panic recovered",
		"goroutine", name,
		"error", fmt.Sprint(r),
		"stack", string(buf[:n]),
	)
	metrics.PanicsTotal.WithLabelValues("goroutine").Inc()
}
