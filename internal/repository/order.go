package repository

import (
	"context"

	"myproject/internal/model"

	"gorm.io/gorm"
)

// OrderRepository 订单数据访问接口
type OrderRepository interface {
	Create(ctx context.Context, order *model.Order) error
	GetByID(ctx context.Context, id uint64) (*model.Order, error)
	Update(ctx context.Context, order *model.Order) error
	UpdateStatus(ctx context.Context, id uint64, from, to int8) error
	Delete(ctx context.Context, id uint64) error
	ListByUserID(ctx context.Context, userID uint64, page, pageSize int, status *int8) ([]*model.Order, int64, error)
}

// OrderStatusLogRepository 订单状态流水数据访问接口。
// 和 OrderRepository 分开：两张表、两套语义（订单可改可删，流水只追加）。
// 只有 Create，是因为暂时没有查流水的入口 —— 需要时再加，不预留空方法。
type OrderStatusLogRepository interface {
	Create(ctx context.Context, log *model.OrderStatusLog) error
}

// orderStatusLogRepository 订单状态流水数据访问实现
type orderStatusLogRepository struct {
	base
}

// NewOrderStatusLog 创建订单状态流水 repository
func NewOrderStatusLog(db *gorm.DB) OrderStatusLogRepository {
	return &orderStatusLogRepository{base: newBase(db)}
}

// Create 追加一条状态流水
func (r *orderStatusLogRepository) Create(ctx context.Context, log *model.OrderStatusLog) error {
	return wrapErr(r.conn(ctx).Create(log).Error)
}

// orderRepository 订单数据访问实现
type orderRepository struct {
	base
}

// NewOrder 创建订单 repository
func NewOrder(db *gorm.DB) OrderRepository {
	return &orderRepository{base: newBase(db)}
}

// Create 创建订单
func (r *orderRepository) Create(ctx context.Context, order *model.Order) error {
	return wrapErr(r.conn(ctx).Create(order).Error)
}

// GetByID 根据 ID 获取订单
func (r *orderRepository) GetByID(ctx context.Context, id uint64) (*model.Order, error) {
	var order model.Order
	if err := r.conn(ctx).First(&order, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &order, nil
}

// Update 更新订单
func (r *orderRepository) Update(ctx context.Context, order *model.Order) error {
	return wrapErr(r.conn(ctx).Save(order).Error)
}

// UpdateStatus 更新订单状态。
//
// 带上原状态做条件更新：service 层是「先查后写」，两个并发请求会读到同一个
// 原状态、各自通过流转校验，最后谁写谁生效（已支付被改成已取消）。
// 把原状态放进 WHERE 后，晚到的那个请求 RowsAffected 为 0，转成 ErrConflict。
//
// RowsAffected 为 0 有两种可能：记录已不存在，或状态已被别人改掉。
// 两者对调用方的处置相同（重新读一次再决定），所以统一为 ErrConflict。
func (r *orderRepository) UpdateStatus(ctx context.Context, id uint64, from, to int8) error {
	res := r.conn(ctx).Model(&model.Order{}).
		Where("id = ? AND status = ?", id, from).
		Update("status", to)
	if res.Error != nil {
		return wrapErr(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrConflict
	}
	return nil
}

// Delete 删除订单
func (r *orderRepository) Delete(ctx context.Context, id uint64) error {
	return wrapErr(r.conn(ctx).Delete(&model.Order{}, id).Error)
}

// ListByUserID 根据用户 ID 分页获取订单列表
func (r *orderRepository) ListByUserID(ctx context.Context, userID uint64, page, pageSize int, status *int8) ([]*model.Order, int64, error) {
	var orders []*model.Order
	var total int64

	countQuery := r.conn(ctx).Model(&model.Order{}).Where("user_id = ?", userID)
	if status != nil {
		countQuery = countQuery.Where("status = ?", *status)
	}
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, wrapErr(err)
	}
	if total == 0 {
		return []*model.Order{}, 0, nil
	}

	// 注意：Count 之后必须重新构造查询，复用同一个 *gorm.DB 会带上 Count 的残留状态
	listQuery := r.conn(ctx).Model(&model.Order{}).Where("user_id = ?", userID)
	if status != nil {
		listQuery = listQuery.Where("status = ?", *status)
	}

	offset := (page - 1) * pageSize
	err := listQuery.Order("id DESC").Offset(offset).Limit(pageSize).Find(&orders).Error
	if err != nil {
		return nil, 0, wrapErr(err)
	}

	return orders, total, nil
}
