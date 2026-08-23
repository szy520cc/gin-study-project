package router

import (
	"fmt"

	"myproject/internal/config"
	"myproject/internal/handler"
	"myproject/internal/middleware"
	"myproject/internal/module"
	"myproject/pkg/health"

	"github.com/gin-gonic/gin"
)

// Setup 构建路由。
//
// 业务路由不在本文件出现：每个模块在 internal/module 下自己声明路由与装配，
// 这里只负责「中间件顺序 + 系统路由 + 遍历模块清单」。
// 新增业务模块不需要改动本文件。
func Setup(cfg *config.Config, healthRegistry *health.Registry, deps module.Deps, modules []module.Register) (*gin.Engine, error) {
	// 校验错误里的字段名改用 json/form tag，全进程一次
	handler.InitValidator()

	gin.SetMode(cfg.Server.Mode)

	r := gin.New()

	// 405 必须显式开启，否则 r.NoMethod 注册的 handler 永远不会被调用，
	// 方法不匹配的请求会被当成 404。
	r.HandleMethodNotAllowed = true

	// 不设置时 gin 信任所有代理，ClientIP() 取 X-Forwarded-For 首段，
	// 可被伪造 —— 按 IP 的限流会被绕过，日志里的来源 IP 也不可信。
	// 传 nil/空表示只信任 RemoteAddr。
	if err := r.SetTrustedProxies(nilIfEmpty(cfg.Server.TrustedProxies)); err != nil {
		return nil, fmt.Errorf("设置可信代理失败: %w", err)
	}

	// 中间件顺序有讲究：
	// RequestID 最先（后续所有日志都依赖它）
	// → 指标（放在限流之前，被限流拒绝的请求也要计入 QPS 与错误率）
	// → 请求体上限（必须早于任何读 Body 的中间件，否则 MaxBytesReader 包不到真实 Body）
	// → 访问日志
	// → Recovery（必须在指标与日志的「内层」：panic 时它先写好 500，
	//   外层的 Metrics/Logger 才能观测到真实状态码。反过来放外层的话，
	//   panic 请求在指标里会记成 200，错误率告警永远不响。gin.Default() 也是这个顺序）
	// → 安全头 → 跨域 → 限流 → 超时
	r.Use(middleware.RequestID())
	r.Use(middleware.Metrics())
	r.Use(middleware.BodyLimit(cfg.Server.MaxBodyBytes))
	r.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{LogBody: cfg.Log.LogBody}))
	r.Use(middleware.Recovery())
	r.Use(middleware.SecurityHeaders(cfg.Server.EnableHSTS))
	r.Use(middleware.Cors(cfg.CORS))

	// 系统路由：探针不带业务前缀，也不属于任何业务模块。
	// 刻意注册在限流与超时之前 —— gin 的 Use 只作用于之后注册的路由，
	// 探针若和业务流量共用令牌桶，过载时 kubelet 会拿到 429：
	// liveness 判失败就重启容器，恰好在最需要实例的时候把实例杀掉。
	// 探测自身的超时由 health.Registry 控制，不需要请求级 Timeout。
	hh := handler.NewHealthHandler(healthRegistry)
	r.GET("/livez", hh.Live)   // 只看进程是否存活，供 K8s liveness 使用
	r.GET("/readyz", hh.Ready) // 探测下游依赖，不健康返回 503，供 K8s readiness 使用
	r.GET("/health", hh.Check) // 兼容旧路径

	if cfg.RateLimit.Enabled {
		r.Use(middleware.RateLimit("global", cfg.RateLimit.RPS, cfg.RateLimit.Burst))
	}
	r.Use(middleware.Timeout(cfg.Server.RequestTimeoutDuration()))

	// 认证与认证类接口限流由 router 统一提供，模块只管往路由上挂：
	// bcrypt 校验是 CPU 密集操作，用全局配额挡不住暴力破解。
	deps.Auth = middleware.Auth(deps.JWT)
	deps.AuthLimit = middleware.NoOp()
	if cfg.RateLimit.Enabled {
		deps.AuthLimit = middleware.RateLimit("auth", cfg.RateLimit.AuthRPS, cfg.RateLimit.AuthBurst)
	}

	v1 := r.Group("/api/v1")
	for _, register := range modules {
		register(v1, deps)
	}

	// 未匹配路由统一返回 JSON，避免 gin 默认的纯文本 404
	r.NoRoute(handler.NotFound)
	r.NoMethod(handler.MethodNotAllowed)

	return r, nil
}

// nilIfEmpty 空切片转 nil：SetTrustedProxies(nil) 才是「不信任任何代理」的语义
func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}
