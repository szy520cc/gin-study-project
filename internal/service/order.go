package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/pkg/errcode"
	"myproject/pkg/transaction"
)

// CreateOrder 创建订单。
//
// 这里不开事务：只有一条 INSERT，单条语句本身就是原子的，
// 而且 GORM 默认已经给单次写操作套了一层事务。真正需要事务的是
// 「一个业务动作对应多次写入」，见 UpdateOrderStatus。
//
// 订单号靠唯一索引兜底：碰撞时换一个号重试，重试仍失败才报错 ——
// 直接把冲突透出去会变成 500，而这本质上是可自愈的内部冲突。
func CreateOrder(ctx context.Context, userID uint64, req *model.CreateOrderRequest) (*model.OrderResponse, error) {
	const maxRetry = 3

	for i := 0; i < maxRetry; i++ {
		order := &model.Order{
			OrderNo:          generateOrderNo(),
			UserID:           userID,
			TotalAmountCents: req.TotalAmountCents,
			Status:           model.OrderStatusPending,
			Remark:           req.Remark,
		}

		err := data.CreateOrder(ctx, order)
		if err == nil {
			return order.ToResponse(), nil
		}
		if !data.IsDuplicate(err) {
			return nil, err
		}
	}

	return nil, errcode.ErrInternal.WithDetails("订单号连续 %d 次冲突", maxRetry)
}

// GetOrder 获取订单详情（带归属校验，只能看自己的）
func GetOrder(ctx context.Context, id, userID uint64) (*model.OrderResponse, error) {
	order, err := getOwnedOrder(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	return order.ToResponse(), nil
}

// UpdateOrderStatus 更新订单状态。
//
// 这是本项目里事务的真实用例：一个业务动作要落两张表 ——
// 改 orders.status，并往 order_status_logs 追加一条流水。两者必须同生同死：
//   - 只改状态没写流水：事后查不出「谁在什么时候把订单改成了已取消」；
//   - 只写流水没改状态：留下一条与事实不符的假记录。
func UpdateOrderStatus(ctx context.Context, id, userID uint64, req *model.UpdateOrderStatusRequest) error {
	order, err := getOwnedOrder(ctx, id, userID)
	if err != nil {
		return err
	}

	if !isValidStatusTransition(order.Status, req.Status) {
		return errcode.ErrInvalidOrderStatus.WithDetails("不允许从 %s 变更为 %s",
			model.GetStatusText(order.Status), model.GetStatusText(req.Status))
	}

	return transaction.Do(ctx, func(ctx context.Context) error {
		// 带原状态做条件更新，影响 0 行说明状态已被并发请求改掉
		affected, err := data.UpdateOrderStatus(ctx, id, order.Status, req.Status)
		if err != nil {
			return err
		}
		if affected == 0 {
			return errcode.ErrInvalidOrderStatus.WithDetails("订单状态已被其他操作变更，请重新查询后重试")
		}

		return data.CreateOrderStatusLog(ctx, &model.OrderStatusLog{
			OrderID:    id,
			FromStatus: order.Status,
			ToStatus:   req.Status,
			OperatorID: userID,
		})
	})
}

// DeleteOrder 删除订单
func DeleteOrder(ctx context.Context, id, userID uint64) error {
	order, err := getOwnedOrder(ctx, id, userID)
	if err != nil {
		return err
	}

	// 只有待支付和已取消的订单可以删除
	if order.Status != model.OrderStatusPending && order.Status != model.OrderStatusCancelled {
		return errcode.ErrOrderCannotDelete.WithDetails("当前状态: %s", model.GetStatusText(order.Status))
	}

	return data.DeleteOrder(ctx, id)
}

// ListOrders 分页获取当前用户的订单
func ListOrders(ctx context.Context, userID uint64, req *model.OrderListRequest) ([]*model.OrderResponse, int64, error) {
	page, pageSize := model.NormalizePage(req.Page, req.PageSize)

	orders, total, err := data.ListOrdersByUser(ctx, userID, req.Status, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	list := make([]*model.OrderResponse, 0, len(orders))
	for _, o := range orders {
		list = append(list, o.ToResponse())
	}
	return list, total, nil
}

// getOwnedOrder 取订单并校验归属。
// 归属不符时返回「不存在」而不是「禁止访问」，避免暴露订单 ID 是否存在。
func getOwnedOrder(ctx context.Context, id, userID uint64) (*model.Order, error) {
	order, err := data.GetOrderByID(ctx, id)
	if err != nil {
		if data.IsNotFound(err) {
			return nil, errcode.ErrOrderNotFound
		}
		return nil, err
	}
	if order.UserID != userID {
		return nil, errcode.ErrOrderNotFound
	}
	return order, nil
}

// generateOrderNo 生成订单号。
//
// 用 crypto/rand 而非 math/rand：math/rand 的序列可预测，订单号能被外部推算出来。
// 秒级时间 + 8 位随机后缀，碰撞由 order_no 唯一索引兜底，CreateOrder 侧会重试。
func generateOrderNo() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败属于系统级异常，退化到纳秒时间戳而不是返回空号
		return fmt.Sprintf("ORD%s%08d", time.Now().Format("20060102150405"), time.Now().Nanosecond()%100000000)
	}
	n := binary.BigEndian.Uint32(b[:]) % 100000000
	return fmt.Sprintf("ORD%s%08d", time.Now().Format("20060102150405"), n)
}

// isValidStatusTransition 检查状态流转是否合法
func isValidStatusTransition(from, to int8) bool {
	allowed := map[int8][]int8{
		model.OrderStatusPending:   {model.OrderStatusPaid, model.OrderStatusCancelled},
		model.OrderStatusPaid:      {model.OrderStatusShipped, model.OrderStatusCancelled},
		model.OrderStatusShipped:   {model.OrderStatusCompleted},
		model.OrderStatusCompleted: {},
		model.OrderStatusCancelled: {},
	}
	for _, s := range allowed[from] {
		if s == to {
			return true
		}
	}
	return false
}
