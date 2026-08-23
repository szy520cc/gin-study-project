// Package module 是业务模块的装配与注册点。
//
// 存在的理由：原来新增一个业务模块要在 4 个聚合器里各登记一遍
// （repository.New / service.New / handler.New / routes.go），
// 外加 router.go 调一次，共 5 处。这些登记没有任何判断逻辑，
// 纯粹是因为三层各自维护了一个带命名字段的聚合 struct。
//
// 现在改成：一个模块一个文件，在文件里自己把三层串起来、自己声明路由，
// 然后在本文件的 All 里加一行。新增模块的登记点从 5 处降到 1 处。
//
// 代价要说清楚：总代码量基本没变（少了聚合器，多了这个包）。
// 换来的是「一个模块的装配方式和路由长什么样，读一个文件就够」，
// 以及新增模块时不必回头改四个不相关的文件。
package module

import (
	"myproject/pkg/auth"
	"myproject/pkg/transaction"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Deps 装配业务模块所需的共享依赖。
// 由 bootstrap 填数据部分，由 router 填中间件部分。
type Deps struct {
	DB  *gorm.DB
	Tx  transaction.Manager
	JWT *auth.JWTManager

	// Auth 认证中间件，需要登录的路由挂它
	Auth gin.HandlerFunc
	// AuthLimit 认证类接口（注册/登录）的严格限流，bcrypt 是 CPU 放大器
	AuthLimit gin.HandlerFunc
}

// Register 一个业务模块：自己装配所需的三层，自己把路由挂到 g 上。
//
// 签名里没有 handler/service/repository 任何类型 ——
// router 只负责提供路由组和中间件，不需要知道模块内部有几层。
type Register func(g *gin.RouterGroup, d Deps)

// All 业务模块清单，供 router.Setup 遍历。
//
// 刻意用手写清单而不是 init 自注册：自注册并不省事（同样是每个模块写一行，
// 只是从这里搬到模块文件里），但会丢掉两样东西 ——
// 「一眼看出系统装了哪些模块」，以及「注释掉一行就能关掉某个模块」的灵活性。
// init 自注册是给跨包插件（database/sql 驱动那种）解耦用的，这里所有模块同包，
// 本来就没有解耦需求。
var All = []Register{
	User,
	Order,
}
