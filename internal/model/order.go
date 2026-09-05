package model

import (
	"fmt"
	"time"
)

// Order 订单模型。
//
// 金额一律用 int64 存「分」，不用 float64：
// float64 无法精确表示 0.1 这类十进制小数，一旦出现累加、折扣、对账，
// 误差必然出现且无法追溯。等到有真实数据后再改，要同时动 DB、API 契约和前端。
type Order struct {
	ID uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	// OrderNo 订单号。唯一索引兜底生成算法的碰撞。
	OrderNo string `json:"order_no" gorm:"type:varchar(64);uniqueIndex;not null"`
	UserID  uint64 `json:"user_id" gorm:"index;not null"`
	// TotalAmountCents 订单金额，单位：分
	TotalAmountCents int64     `json:"total_amount_cents" gorm:"column:total_amount_cents;type:bigint;not null"`
	Status           int8      `json:"status" gorm:"type:tinyint;default:0;comment:0-待支付 1-已支付 2-已发货 3-已完成 4-已取消"`
	Remark           string    `json:"remark" gorm:"type:varchar(255)"`
	CreatedAt        time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName 指定表名
func (Order) TableName() string {
	return "orders"
}

// OrderStatusLog 订单状态流水。
//
// 单独成表而不是往 orders 上加字段：状态变更是一串历史，一行记录装不下。
// 它的存在也是订单状态变更必须走事务的原因 —— 改状态和写流水要么都成功，
// 要么都不发生，否则事后无法回答「谁把订单改成了已取消」。
type OrderStatusLog struct {
	ID         uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	OrderID    uint64 `json:"order_id" gorm:"index;not null"`
	FromStatus int8   `json:"from_status" gorm:"type:tinyint;not null"`
	ToStatus   int8   `json:"to_status" gorm:"type:tinyint;not null"`
	// OperatorID 操作人 user_id
	OperatorID uint64    `json:"operator_id" gorm:"not null"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
}

// TableName 指定表名
func (OrderStatusLog) TableName() string {
	return "order_status_logs"
}

// OrderStatus 订单状态
const (
	OrderStatusPending   int8 = 0 // 待支付
	OrderStatusPaid      int8 = 1 // 已支付
	OrderStatusShipped   int8 = 2 // 已发货
	OrderStatusCompleted int8 = 3 // 已完成
	OrderStatusCancelled int8 = 4 // 已取消
)

// CreateOrderRequest 创建订单请求。
// 金额以「分」为单位传入，避免 JSON 浮点数在传输和解析过程中丢精度。
type CreateOrderRequest struct {
	TotalAmountCents int64  `json:"total_amount_cents" binding:"required,gt=0"`
	Remark           string `json:"remark" binding:"omitempty,max=255"`
}

// UpdateOrderStatusRequest 更新订单状态请求
type UpdateOrderStatusRequest struct {
	Status int8 `json:"status" binding:"required,oneof=0 1 2 3 4"`
}

// OrderListRequest 订单列表请求
// Status 用指针：以区分「不筛选」与「筛选待支付(0)」——否则待支付订单永远筛不出来。
type OrderListRequest struct {
	Page     int   `form:"page" binding:"omitempty,min=1"`
	PageSize int   `form:"page_size" binding:"omitempty,min=1,max=100"`
	Status   *int8 `form:"status" binding:"omitempty,oneof=0 1 2 3 4"`
}

// OrderResponse 订单响应。
// 同时给出分和格式化后的字符串：前端展示直接用 total_amount_text，
// 需要计算时用 total_amount_cents，两边都不碰浮点。
type OrderResponse struct {
	ID               uint64    `json:"id"`
	OrderNo          string    `json:"order_no"`
	UserID           uint64    `json:"user_id"`
	TotalAmountCents int64     `json:"total_amount_cents"`
	TotalAmountText  string    `json:"total_amount_text"`
	Status           int8      `json:"status"`
	StatusText       string    `json:"status_text"`
	Remark           string    `json:"remark"`
	CreatedAt        time.Time `json:"created_at"`
}

// ToResponse 将 Order 转换为响应体。
// 转换函数放在 model 上（与 User.ToResponse 一致），service 直接调，
// 不用每个模块在 service 里再抄一个 toXxxResponse。
func (o *Order) ToResponse() *OrderResponse {
	return &OrderResponse{
		ID:               o.ID,
		OrderNo:          o.OrderNo,
		UserID:           o.UserID,
		TotalAmountCents: o.TotalAmountCents,
		TotalAmountText:  FormatCents(o.TotalAmountCents),
		Status:           o.Status,
		StatusText:       GetStatusText(o.Status),
		Remark:           o.Remark,
		CreatedAt:        o.CreatedAt,
	}
}

// FormatCents 把「分」格式化成两位小数的金额字符串，全程整数运算不引入浮点误差
func FormatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

// GetStatusText 获取状态文本
func GetStatusText(status int8) string {
	switch status {
	case OrderStatusPending:
		return "待支付"
	case OrderStatusPaid:
		return "已支付"
	case OrderStatusShipped:
		return "已发货"
	case OrderStatusCompleted:
		return "已完成"
	case OrderStatusCancelled:
		return "已取消"
	default:
		return "未知"
	}
}
