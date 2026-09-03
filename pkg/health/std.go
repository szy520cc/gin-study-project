package health

import (
	"context"
	"time"
)

// 本文件是包级默认注册表。
//
// 探针注册表进程内只有一份，与进程生命周期等长。做成包级单例后
// bootstrap 里 health.RegisterFunc("database", ...)、HTTP 层里 health.Check(ctx)
// 都是直接调用，不需要把 *Registry 从 bootstrap 一路传到 HTTP 层。
// 需要多份注册表（比如测试里要隔离）时直接用 NewRegistry。

var std = NewRegistry(2*time.Second, 2*time.Second)

// Init 重设默认注册表的探测超时与缓存时长，只应在启动阶段调用
func Init(timeout, cacheTTL time.Duration) { std = NewRegistry(timeout, cacheTTL) }

// RegisterFunc 往默认注册表注册依赖
func RegisterFunc(name string, fn func(ctx context.Context) error) { std.RegisterFunc(name, fn) }

// Check 用默认注册表探测所有依赖
func Check(ctx context.Context) Report { return std.Check(ctx) }

// SetExposeErrors 设置默认注册表是否对外暴露下游原始错误
func SetExposeErrors(v bool) { std.SetExposeErrors(v) }

// StartDraining 标记默认注册表进入摘流阶段
func StartDraining() { std.StartDraining() }

// IsDraining 默认注册表是否处于摘流阶段
func IsDraining() bool { return std.IsDraining() }
