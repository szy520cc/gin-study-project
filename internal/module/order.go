package module

import (
	"myproject/internal/handler"
	"myproject/internal/repository"
	"myproject/internal/service"

	"github.com/gin-gonic/gin"
)

// Order 装配 order 模块：repository → service → handler，并注册路由。
func Order(g *gin.RouterGroup, d Deps) {
	OrderWith(g, d, service.NewOrderService(
		repository.NewOrder(d.DB),
		repository.NewOrderStatusLog(d.DB),
		d.Tx,
	))
}

// OrderWith 用指定的 service 注册 order 路由（测试可注入 stub，无需数据库）。
func OrderWith(g *gin.RouterGroup, d Deps, svc service.OrderService) {
	h := handler.NewOrderHandler(svc)

	orders := g.Group("/orders", d.Auth)
	{
		orders.POST("", h.CreateOrder)
		orders.GET("", h.ListOrders)
		orders.GET("/:id", h.GetOrder)
		orders.PUT("/:id/status", h.UpdateOrderStatus)
		orders.DELETE("/:id", h.DeleteOrder)
	}
}
