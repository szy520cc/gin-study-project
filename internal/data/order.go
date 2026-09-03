package data

import (
	"context"

	"myproject/internal/model"

	"gorm.io/gorm"
)

// CreateOrder 插入一条订单
func CreateOrder(ctx context.Context, order *model.Order) error {
	return connDb(ctx).Create(order).Error
}

// GetOrderByID 按主键取订单，不存在时返回 gorm.ErrRecordNotFound。
// 归属校验属于业务规则，由 service 判断。
func GetOrderByID(ctx context.Context, id uint64) (*model.Order, error) {
	var order model.Order
	if err := connDb(ctx).First(&order, id).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

// UpdateOrderStatus 带原状态做条件更新，返回实际影响行数。
//
// 把 from 放进 WHERE 是乐观并发控制：调用方是「先查后写」，两个并发请求会读到
// 同一个原状态、各自通过流转校验，晚到的那个在这里影响 0 行。
// 调用方必须检查返回的行数，0 表示状态已被他人改掉。
func UpdateOrderStatus(ctx context.Context, id uint64, from, to int8) (int64, error) {
	res := connDb(ctx).Model(&model.Order{}).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	return res.RowsAffected, res.Error
}

// CreateOrderStatusLog 追加一条状态变更流水
func CreateOrderStatusLog(ctx context.Context, log *model.OrderStatusLog) error {
	return connDb(ctx).Create(log).Error
}

// DeleteOrder 按主键删除订单
func DeleteOrder(ctx context.Context, id uint64) error {
	return connDb(ctx).Delete(&model.Order{}, id).Error
}

// ListOrdersByUser 分页查询某用户的订单，status 为 nil 表示不限状态。
// 返回当页数据与总数；page/pageSize 由 service 归一化后传入。
func ListOrdersByUser(ctx context.Context, userID uint64, status *int8, page, pageSize int) ([]*model.Order, int64, error) {
	// 同一组条件要用两次（count + list）。必须重新构造查询：
	// Count 会在链上留下残留状态，复用同一个 *gorm.DB 会污染后续查询。
	query := func() *gorm.DB {
		q := connDb(ctx).Model(&model.Order{}).Where("user_id = ?", userID)
		if status != nil {
			q = q.Where("status = ?", *status)
		}
		return q
	}

	var total int64
	if err := query().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	var orders []*model.Order
	err := query().Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&orders).Error
	if err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}
