// Package router 构建 gin 引擎：引擎基础设置 + 中间件顺序 + 全部路由表。
//
// 读法：先看 Setup，它按挂载顺序把下面几个函数串起来，那就是「服务的形状」；
// 再往下是中间件装配与路由表。新增接口就在对应的 registerXxx 里加一行。
package router

import (
	"fmt"

	"myproject/internal/config"
	"myproject/internal/controller"
	"myproject/internal/middleware"

	"github.com/gin-gonic/gin"
)

// Setup 构建路由
func Setup(cfg *config.Config) (*gin.Engine, error) {
	// 校验错误里的字段名改用 json/form tag，全进程一次
	controller.InitValidator()

	gin.SetMode(cfg.Server.Mode)

	r := gin.New()

	// 405 必须显式开启，否则 r.NoMethod 注册的 handler 永远不会被调用，
	// 方法不匹配的请求会被当成 404。
	r.HandleMethodNotAllowed = true

	if err := setTrustedProxies(r, cfg); err != nil {
		return nil, err
	}

	// 挂载顺序即执行顺序，且探针必须夹在两组中间件之间 —— 详见各函数注释。
	useBaseMiddleware(r, cfg)
	registerSystemRoutes(r)
	useThrottleMiddleware(r, cfg)
	registerAPIRoutes(r, cfg)
	registerFallbackRoutes(r)

	return r, nil
}

// setTrustedProxies 设置可信代理。
//
// 不设置时 gin 信任所有代理，ClientIP() 取 X-Forwarded-For 首段，可被伪造 ——
// 按 IP 的限流会被绕过，日志里的来源 IP 也不可信。
// 传 nil 才是「不信任任何代理，只认 RemoteAddr」的语义，空切片不行。
func setTrustedProxies(r *gin.Engine, cfg *config.Config) error {
	proxies := cfg.Server.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		return fmt.Errorf("设置可信代理失败: %w", err)
	}
	return nil
}

// useBaseMiddleware 挂载与限流无关的基础中间件。顺序有讲究：
//
//	RequestID 最先（后续所有日志都依赖它）
//	→ 指标（放在限流之前，被限流拒绝的请求也要计入 QPS 与错误率）
//	→ 请求体上限（必须早于任何读 Body 的中间件，否则 MaxBytesReader 包不到真实 Body）
//	→ 访问日志
//	→ Recovery（必须在指标与日志的「内层」：panic 时它先写好 500，
//	  外层的 Metrics/Logger 才能观测到真实状态码。放外层的话 panic 请求
//	  在指标里会记成 200，错误率告警永远不响。gin.Default() 也是这个顺序）
//	→ 安全头 → 跨域
func useBaseMiddleware(r *gin.Engine, cfg *config.Config) {
	r.Use(middleware.RequestID())
	r.Use(middleware.Metrics())
	r.Use(middleware.BodyLimit(cfg.Server.MaxBodyBytes))
	r.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{LogBody: cfg.Log.LogBody}))
	r.Use(middleware.Recovery())
	r.Use(middleware.SecurityHeaders(cfg.Server.EnableHSTS))
	r.Use(middleware.Cors(cfg.CORS))
}

// useThrottleMiddleware 挂载限流与超时。
//
// 必须在 registerSystemRoutes 之后调用：gin 的 Use 只作用于之后注册的路由，
// 探针若和业务流量共用令牌桶，过载时 kubelet 会拿到 429 ——
// liveness 判失败就重启容器，恰好在最需要实例的时候把实例杀掉。
func useThrottleMiddleware(r *gin.Engine, cfg *config.Config) {
	if cfg.RateLimit.Enabled {
		r.Use(middleware.RateLimit("global", cfg.RateLimit.RPS, cfg.RateLimit.Burst))
	}
	r.Use(middleware.Timeout(cfg.Server.RequestTimeoutDuration()))
}

// ---------- 路由表 ----------
//
// 新增接口就在对应的 registerXxx 里加一行，新增模块就加一个 registerXxx 函数
// 并在 registerAPIRoutes 里调一次。全部路由集中在一处的好处是
// 「这个服务对外提供什么」有唯一答案，不用翻 N 个模块文件去拼。

// registerSystemRoutes 系统探针。
// 刻意在限流与超时之前注册，原因见 useThrottleMiddleware 的注释。
func registerSystemRoutes(r *gin.Engine) {
	r.GET("/livez", controller.Live)   // 只看进程是否存活，供 K8s liveness 使用
	r.GET("/readyz", controller.Ready) // 探测下游依赖，不健康返回 503，供 K8s readiness 使用
	r.GET("/health", controller.Ready) // 兼容旧路径
}

// registerAPIRoutes 业务接口，统一挂在 /api/v1 下
func registerAPIRoutes(r *gin.Engine, cfg *config.Config) {
	auth := middleware.Auth()

	// 认证类接口单独一档更严的限流：bcrypt 校验是 CPU 密集操作，
	// 用全局配额挡不住暴力破解。限流关闭时用 NoOp 占位，避免调用处写分支。
	authLimit := middleware.NoOp()
	if cfg.RateLimit.Enabled {
		authLimit = middleware.RateLimit("auth", cfg.RateLimit.AuthRPS, cfg.RateLimit.AuthBurst)
	}

	v1 := r.Group("/api/v1")
	registerUser(v1, auth, authLimit)
	registerOrder(v1, auth)
}

// registerUser 用户模块路由
func registerUser(g *gin.RouterGroup, auth, authLimit gin.HandlerFunc) {
	g.POST("/users/register", authLimit, controller.Register)
	g.POST("/users/login", authLimit, controller.Login)

	users := g.Group("/users", auth)
	{
		users.GET("/profile", controller.GetProfile)
		users.PUT("/profile", controller.UpdateProfile)
		users.GET("", controller.ListUsers)
		users.GET("/:id", controller.GetUser)
	}
}

// registerOrder 订单模块路由
func registerOrder(g *gin.RouterGroup, auth gin.HandlerFunc) {
	orders := g.Group("/orders", auth)
	{
		orders.POST("", controller.CreateOrder)
		orders.GET("", controller.ListOrders)
		orders.GET("/:id", controller.GetOrder)
		orders.PUT("/:id/status", controller.UpdateOrderStatus)
		orders.DELETE("/:id", controller.DeleteOrder)
	}
}

// registerFallbackRoutes 未匹配路由统一返回 JSON，避免 gin 默认的纯文本 404
func registerFallbackRoutes(r *gin.Engine) {
	r.NoRoute(controller.NotFound)
	r.NoMethod(controller.MethodNotAllowed)
}
