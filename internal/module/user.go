package module

import (
	"myproject/internal/handler"
	"myproject/internal/middleware"
	"myproject/internal/repository"
	"myproject/internal/service"

	"github.com/gin-gonic/gin"
)

// User 装配 user 模块：repository → service → handler，并注册路由。
func User(g *gin.RouterGroup, d Deps) {
	UserWith(g, d, service.NewUserService(repository.NewUser(d.DB), d.Tx))
}

// UserWith 用指定的 service 注册 user 路由。
//
// 单独留这个入口是为了测试：注入 stub service 就能起一个不连数据库的完整 HTTP 栈，
// 路由表与线上完全一致（不必在测试里重抄一遍路径）。
func UserWith(g *gin.RouterGroup, d Deps, svc service.UserService) {
	h := handler.NewUserHandler(svc, d.JWT)

	// 公开路由。authLimit 是给注册/登录用的更严限流：
	// bcrypt 是 CPU 密集操作，用普通接口的配额挡不住暴力破解，也挡不住 CPU 打满。
	g.POST("/users/register", d.AuthLimit, h.Register)
	g.POST("/users/login", d.AuthLimit, h.Login)

	// 需要认证
	users := g.Group("/users", d.Auth)
	{
		users.GET("/profile", h.GetProfile)
		users.PUT("/profile", h.UpdateProfile)
		users.GET("", h.ListUsers)
		users.GET("/:id", h.GetUser)

		// 删除用户仅允许操作自己：原实现任何登录用户都能删除任意账号。
		// 后续接入角色体系后，这里可替换为 middleware.RequireRole("admin")。
		users.DELETE("/:id", middleware.SelfOnly("id"), h.DeleteUser)
	}
}
