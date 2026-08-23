package handler

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/service"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// OrderHandler 订单处理器
type OrderHandler struct {
	orderService service.OrderService
}

// NewOrderHandler 创建订单处理器实例
func NewOrderHandler(orderService service.OrderService) *OrderHandler {
	return &OrderHandler{orderService: orderService}
}

// CreateOrder 创建订单
// @Summary 创建订单
// @Tags 订单
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body model.CreateOrderRequest true "订单信息"
// @Success 200 {object} response.Response{data=model.OrderResponse}
// @Router /api/v1/orders [post]
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req model.CreateOrderRequest
	if !bindJSON(c, &req) {
		return
	}

	order, err := h.orderService.Create(c.Request.Context(), userID, &req)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, order)
}

// GetOrder 获取订单详情
// @Summary 获取订单详情
// @Tags 订单
// @Produce json
// @Security Bearer
// @Param id path int true "订单ID"
// @Success 200 {object} response.Response{data=model.OrderResponse}
// @Router /api/v1/orders/{id} [get]
func (h *OrderHandler) GetOrder(c *gin.Context) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, ok := pathID(c, "id")
	if !ok {
		return
	}

	// 带归属校验：原实现任何登录用户都能查别人的订单
	order, err := h.orderService.GetByIDForUser(c.Request.Context(), id, userID)
	if err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, order)
}

// UpdateOrderStatus 更新订单状态
// @Summary 更新订单状态
// @Tags 订单
// @Accept json
// @Produce json
// @Security Bearer
// @Param id path int true "订单ID"
// @Param request body model.UpdateOrderStatusRequest true "状态信息"
// @Success 200 {object} response.Response
// @Router /api/v1/orders/{id}/status [put]
func (h *OrderHandler) UpdateOrderStatus(c *gin.Context) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, ok := pathID(c, "id")
	if !ok {
		return
	}

	var req model.UpdateOrderStatusRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := h.orderService.UpdateStatus(c.Request.Context(), id, userID, &req); err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, nil)
}

// DeleteOrder 删除订单
// @Summary 删除订单
// @Tags 订单
// @Produce json
// @Security Bearer
// @Param id path int true "订单ID"
// @Success 200 {object} response.Response
// @Router /api/v1/orders/{id} [delete]
func (h *OrderHandler) DeleteOrder(c *gin.Context) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	id, ok := pathID(c, "id")
	if !ok {
		return
	}

	if err := h.orderService.Delete(c.Request.Context(), id, userID); err != nil {
		response.Error(c, err)
		return
	}

	response.Success(c, nil)
}

// ListOrders 获取当前用户订单列表
// @Summary 获取订单列表
// @Tags 订单
// @Produce json
// @Security Bearer
// @Param page query int false "页码"
// @Param page_size query int false "每页数量，最大 100"
// @Param status query int false "订单状态"
// @Success 200 {object} response.Response{data=response.ListData}
// @Router /api/v1/orders [get]
func (h *OrderHandler) ListOrders(c *gin.Context) {
	userID, ok := middleware.RequireUserID(c)
	if !ok {
		return
	}

	var req model.OrderListRequest
	if !bindQuery(c, &req) {
		return
	}

	orders, total, err := h.orderService.ListByUserID(c.Request.Context(), userID, &req)
	if err != nil {
		response.Error(c, err)
		return
	}

	page, pageSize := model.PageRequest{Page: req.Page, PageSize: req.PageSize}.Normalize()
	response.SuccessList(c, orders, total, page, pageSize)
}
