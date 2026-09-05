// Package router 构建 gin 引擎：引擎基础设置 + 中间件顺序 + 全部路由表。
//
// 读法：先看 Setup，它按挂载顺序把下面几个函数串起来，那就是「服务的形状」；
// 再往下是中间件装配与路由表。新增接口就在对应的 registerXxx 里加一行。
package router

import (
	"fmt"
	"io/fs"
	"net/http"

	"myproject/internal/config"
	"myproject/internal/controller"
	"myproject/internal/middleware"
	"myproject/internal/web"

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
	registerAdminUI(r)
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

// registerAdminUI 把后台管理 UI（Amis 单页 + schema 文件）挂在 /admin 下。
//
// 设计取舍：后台 UI 跟主 API 共享业务 server（8080），而不是塞进 pkg/admin
// 的 9090 metrics 端口 —— 后者面向 Prometheus/运维，UI 面向运营/开发，
// 职责完全不同（CORS、鉴权、访问频率都不同），混在一起两边都不好扩展。
//
// 路径规划：
//   /admin/             → index.html（Amis 容器）
//   /admin/login        → login.html（独立登录页，跳过 amis 渲染壳）
//   /admin/static/...   → SDK / 主题 css（go:embed 一并打入二进制）
//   /admin/pages/*.json → Amis schema 文件（直接吐 JSON）
func registerAdminUI(r *gin.Engine) {
	// /admin/login 必须单独存在：amis 的 init 逻辑会执行登录 API，
	// 在登录前没有 token，登录页不能依赖 amis-renderer 自身的初始化流程，
	// 也不希望被 "/ -> index.html -> 自动跳 login" 走两次路由。
	r.GET("/admin/login", func(c *gin.Context) {
		data, err := web.ReadLogin()
		if err != nil {
			c.String(500, "login page missing: %v", err)
			return
		}
		c.Data(200, "text/html; charset=utf-8", data)
	})

	r.GET("/admin", func(c *gin.Context) { c.Redirect(302, "/admin/") })
	r.GET("/admin/", func(c *gin.Context) {
		data, err := web.ReadIndex()
		if err != nil {
			c.String(500, "admin index missing: %v", err)
			return
		}
		c.Data(200, "text/html; charset=utf-8", data)
	})

	// schema JSON 单独走一条路径而不是 StaticFS：
	// - StaticFS 会把整个目录树都挂上去，未来误丢一个 .json 也会被无脑暴露；
	//   显式 HandleFunc 走我们自己写过的 handler，至少要过一次内部白名单。
	r.GET("/admin/pages/*.json", func(c *gin.Context) {
		name := c.Param(".json") // 含前导点，例如 "/login.json"
		data, err := web.ReadSchema(name)
		if err != nil {
			c.String(404, "schema not found: %s", name)
			return
		}
		c.Data(200, "application/json; charset=utf-8", data)
	})

	// 静态资源走 StaticFS：sdk.js / css / favicon 等。
	// 注意要挂在 /admin/static 前缀下，否则 /admin/ 下的 GET 会和 StaticFS 抢路径
	// —— gin 路由匹配按注册顺序，/admin/ 先注册就先命中。
	r.StaticFS("/admin/static", http.FS(web.StaticFS()))
}

// mustSub 是 fs.Sub 的 panic 包装：embed.FS 在编译期已知子目录是否存在，
// 这里 sub 出错只可能是因为写错目录名，应当在测试期就暴露。
//
// 当前路由里没有直接用到 fs.Sub（已抽到 internal/web），保留是给以后
// 想再切子目录时一个一致的报错样式。
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(fmt.Sprintf("router: embed sub %q: %v", dir, err))
	}
	return sub
}
