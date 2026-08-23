package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"myproject/internal/model"
	"myproject/internal/repository"
	"myproject/pkg/errcode"
	"myproject/pkg/transaction"
)

// OrderService 订单业务逻辑接口
type OrderService interface {
	Create(ctx context.Context, userID uint64, req *model.CreateOrderRequest) (*model.OrderResponse, error)
	// GetByIDForUser 带归属校验：只允许查询自己的订单。
	// 不提供「不校验归属」的版本 —— 那个方法零调用，留着只是等人误用成越权入口。
	GetByIDForUser(ctx context.Context, id, userID uint64) (*model.OrderResponse, error)
	UpdateStatus(ctx context.Context, id, userID uint64, req *model.UpdateOrderStatusRequest) error
	Delete(ctx context.Context, id, userID uint64) error
	ListByUserID(ctx context.Context, userID uint64, req *model.OrderListRequest) ([]*model.OrderResponse, int64, error)
}

// orderService 订单业务逻辑实现
type orderService struct {
	orderRepo repository.OrderRepository
	logRepo   repository.OrderStatusLogRepository
	tx        transaction.Manager
}

// NewOrderService 创建订单业务逻辑实例
func NewOrderService(orderRepo repository.OrderRepository, logRepo repository.OrderStatusLogRepository, tx transaction.Manager) OrderService {
	return &orderService{orderRepo: orderRepo, logRepo: logRepo, tx: tx}
}

// Create 创建订单。
//
// 这里不开事务：只有一条 INSERT，单条语句本身就是原子的，
// 而且 GORM 默认已经给单次写操作套了一层事务（未开 SkipDefaultTransaction）。
// 再包一层 s.tx.Do 只会多一次 BEGIN/COMMIT 往返，换不到任何一致性保证。
// 真正需要事务的是「一个业务动作对应多次写入」，见 UpdateStatus。
//
// 订单号靠唯一索引兜底：碰撞时换一个号重试，重试仍失败才报错 ——
// 直接把 ErrConflict 透出去会变成 500，而这本质上是可自愈的内部冲突。
func (s *orderService) Create(ctx context.Context, userID uint64, req *model.CreateOrderRequest) (*model.OrderResponse, error) {
	const maxRetry = 3

	var order *model.Order
	for i := 0; i < maxRetry; i++ {
		order = &model.Order{
			OrderNo:          generateOrderNo(),
			UserID:           userID,
			TotalAmountCents: req.TotalAmountCents,
			Status:           model.OrderStatusPending,
			Remark:           req.Remark,
		}

		err := s.orderRepo.Create(ctx, order)
		if err == nil {
			return toOrderResponse(order), nil
		}
		if !errors.Is(err, repository.ErrConflict) {
			return nil, err
		}
	}

	return nil, errcode.ErrInternal.WithDetails("订单号连续 %d 次冲突", maxRetry)
}

// GetByIDForUser 获取订单并校验归属
func (s *orderService) GetByIDForUser(ctx context.Context, id, userID uint64) (*model.OrderResponse, error) {
	order, err := s.getOwnedOrder(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	return toOrderResponse(order), nil
}

// UpdateStatus 更新订单状态。
//
// 这是本项目里事务的真实用例：一个业务动作要落两张表 ——
// 改 orders.status，并往 order_status_logs 追加一条流水。
// 两者必须同生同死：
//   - 只改状态没写流水：事后查不出「谁在什么时候把订单改成了已取消」；
//   - 只写流水没改状态：留下一条与事实不符的假记录。
//
// s.tx.Do 把事务句柄放进 ctx，repository 的 conn(ctx) 会自动认领，
// 所以 service 全程不碰 *gorm.DB，repository 也不需要为事务写第二套方法。
// 闭包里任一步返回 error（包括 UpdateStatus 的 ErrConflict）都整体回滚。
func (s *orderService) UpdateStatus(ctx context.Context, id, userID uint64, req *model.UpdateOrderStatusRequest) error {
	order, err := s.getOwnedOrder(ctx, id, userID)
	if err != nil {
		return err
	}

	if !isValidStatusTransition(order.Status, req.Status) {
		return errcode.ErrInvalidOrderStatus.WithDetails("不允许从 %s 变更为 %s",
			model.GetStatusText(order.Status), model.GetStatusText(req.Status))
	}

	err = s.tx.Do(ctx, func(ctx context.Context) error {
		// 带原状态做条件更新，避免并发下两个请求都通过校验后互相覆盖
		if err := s.orderRepo.UpdateStatus(ctx, id, order.Status, req.Status); err != nil {
			return err
		}
		return s.logRepo.Create(ctx, &model.OrderStatusLog{
			OrderID:    id,
			FromStatus: order.Status,
			ToStatus:   req.Status,
			OperatorID: userID,
		})
	})
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return errcode.ErrInvalidOrderStatus.WithDetails("订单状态已被其他操作变更，请重新查询后重试")
		}
		return err
	}
	return nil
}

// Delete 删除订单
func (s *orderService) Delete(ctx context.Context, id, userID uint64) error {
	order, err := s.getOwnedOrder(ctx, id, userID)
	if err != nil {
		return err
	}

	// 只有待支付和已取消的订单可以删除
	if order.Status != model.OrderStatusPending && order.Status != model.OrderStatusCancelled {
		return errcode.ErrOrderCannotDelete.WithDetails("当前状态: %s", model.GetStatusText(order.Status))
	}

	if err := s.orderRepo.Delete(ctx, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return errcode.ErrOrderNotFound
		}
		return err
	}
	return nil
}

// ListByUserID 获取用户订单列表
func (s *orderService) ListByUserID(ctx context.Context, userID uint64, req *model.OrderListRequest) ([]*model.OrderResponse, int64, error) {
	page, pageSize := normalizePage(req.Page, req.PageSize)

	orders, total, err := s.orderRepo.ListByUserID(ctx, userID, page, pageSize, req.Status)
	if err != nil {
		return nil, 0, err
	}

	responses := make([]*model.OrderResponse, 0, len(orders))
	for _, order := range orders {
		responses = append(responses, toOrderResponse(order))
	}

	return responses, total, nil
}

func (s *orderService) getOrder(ctx context.Context, id uint64) (*model.Order, error) {
	order, err := s.orderRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, errcode.ErrOrderNotFound
		}
		return nil, err
	}
	return order, nil
}

// getOwnedOrder 取订单并校验归属。
// 归属不符时返回「不存在」而不是「禁止访问」，避免暴露订单 ID 是否存在。
func (s *orderService) getOwnedOrder(ctx context.Context, id, userID uint64) (*model.Order, error) {
	order, err := s.getOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		return nil, errcode.ErrOrderNotFound
	}
	return order, nil
}

// toOrderResponse 转换为订单响应
func toOrderResponse(order *model.Order) *model.OrderResponse {
	return &model.OrderResponse{
		ID:               order.ID,
		OrderNo:          order.OrderNo,
		UserID:           order.UserID,
		TotalAmountCents: order.TotalAmountCents,
		TotalAmountText:  model.FormatCents(order.TotalAmountCents),
		Status:           order.Status,
		StatusText:       model.GetStatusText(order.Status),
		Remark:           order.Remark,
		CreatedAt:        order.CreatedAt,
	}
}

// generateOrderNo 生成订单号。
//
// 用 crypto/rand 而非 math/rand：math/rand 的序列可预测，
// 订单号能被外部推算出来（配合归属校验虽然拿不到数据，但泄露了下单量）。
// 秒级时间 + 8 位随机后缀，碰撞由 order_no 唯一索引兜底，Create 侧会重试。
func generateOrderNo() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败属于系统级异常，退化到纳秒时间戳而不是返回空号
		return fmt.Sprintf("ORD%s%08d", time.Now().Format("20060102150405"), time.Now().Nanosecond()%100000000)
	}
	n := binary.BigEndian.Uint32(b[:]) % 100000000
	return fmt.Sprintf("ORD%s%08d", time.Now().Format("20060102150405"), n)
}

// isValidStatusTransition 检查状态转换是否合法
func isValidStatusTransition(from, to int8) bool {
	validTransitions := map[int8][]int8{
		model.OrderStatusPending:   {model.OrderStatusPaid, model.OrderStatusCancelled},
		model.OrderStatusPaid:      {model.OrderStatusShipped, model.OrderStatusCancelled},
		model.OrderStatusShipped:   {model.OrderStatusCompleted},
		model.OrderStatusCompleted: {},
		model.OrderStatusCancelled: {},
	}

	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}
