package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"myproject/internal/model"
	"myproject/pkg/errcode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createOrder 建一个订单并返回响应
func createOrder(t *testing.T, token string, cents int64) model.OrderResponse {
	t.Helper()
	code, resp := do(t, http.MethodPost, "/api/v1/orders", token,
		model.CreateOrderRequest{TotalAmountCents: cents, Remark: "integration"})
	require.Equal(t, http.StatusOK, code, "建单失败: %s %s", resp.Message, resp.Details)

	var order model.OrderResponse
	require.NoError(t, json.Unmarshal(resp.Data, &order))
	require.NotZero(t, order.ID)
	return order
}

// TestOrderFlow 建单 → 列表 → 详情 → 改状态（含流水）→ 删除
func TestOrderFlow(t *testing.T) {
	requireDB(t)
	_, token := newUser(t)

	order := createOrder(t, token, 1999)

	t.Run("金额按分存储且格式化正确", func(t *testing.T) {
		assert.Equal(t, int64(1999), order.TotalAmountCents)
		assert.Equal(t, "19.99", order.TotalAmountText)
		assert.Equal(t, model.OrderStatusPending, order.Status)

		var row model.Order
		require.NoError(t, testDB.First(&row, order.ID).Error)
		assert.Equal(t, int64(1999), row.TotalAmountCents)
	})

	t.Run("列表能查到", func(t *testing.T) {
		code, resp := do(t, http.MethodGet, "/api/v1/orders?page=1&page_size=10", token, nil)
		require.Equal(t, http.StatusOK, code)
		var list struct {
			List  []model.OrderResponse `json:"list"`
			Total int64                 `json:"total"`
		}
		require.NoError(t, json.Unmarshal(resp.Data, &list))
		assert.Equal(t, int64(1), list.Total)
		require.Len(t, list.List, 1)
		assert.Equal(t, order.OrderNo, list.List[0].OrderNo)
	})

	t.Run("按状态筛选待支付", func(t *testing.T) {
		code, resp := do(t, http.MethodGet, "/api/v1/orders?status=0", token, nil)
		require.Equal(t, http.StatusOK, code)
		var list struct {
			Total int64 `json:"total"`
		}
		require.NoError(t, json.Unmarshal(resp.Data, &list))
		// status=0 是零值，用指针接收才筛得出来，这里正是在验这件事
		assert.Equal(t, int64(1), list.Total)
	})

	t.Run("改状态同时落流水", func(t *testing.T) {
		code, _ := do(t, http.MethodPut, fmt.Sprintf("/api/v1/orders/%d/status", order.ID), token,
			model.UpdateOrderStatusRequest{Status: model.OrderStatusPaid})
		require.Equal(t, http.StatusOK, code)

		var row model.Order
		require.NoError(t, testDB.First(&row, order.ID).Error)
		assert.Equal(t, model.OrderStatusPaid, row.Status)

		var logs []model.OrderStatusLog
		require.NoError(t, testDB.Where("order_id = ?", order.ID).Find(&logs).Error)
		require.Len(t, logs, 1, "状态变更必须留一条流水")
		assert.Equal(t, model.OrderStatusPending, logs[0].FromStatus)
		assert.Equal(t, model.OrderStatusPaid, logs[0].ToStatus)
	})

	t.Run("非法流转被拒绝且不留流水", func(t *testing.T) {
		code, resp := do(t, http.MethodPut, fmt.Sprintf("/api/v1/orders/%d/status", order.ID), token,
			model.UpdateOrderStatusRequest{Status: model.OrderStatusCompleted})
		// 状态流转非法与并发冲突都归 409（见 errcode.ErrInvalidOrderStatus 的注释）
		assert.Equal(t, http.StatusConflict, code)
		assert.Equal(t, errcode.ErrInvalidOrderStatus.Code(), resp.Code)

		var n int64
		testDB.Model(&model.OrderStatusLog{}).Where("order_id = ?", order.ID).Count(&n)
		assert.Equal(t, int64(1), n, "被拒绝的流转不应写流水")
	})

	t.Run("已支付订单不允许删除", func(t *testing.T) {
		code, resp := do(t, http.MethodDelete, fmt.Sprintf("/api/v1/orders/%d", order.ID), token, nil)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, errcode.ErrOrderCannotDelete.Code(), resp.Code)
	})

	t.Run("待支付订单可以删除", func(t *testing.T) {
		pending := createOrder(t, token, 500)
		code, _ := do(t, http.MethodDelete, fmt.Sprintf("/api/v1/orders/%d", pending.ID), token, nil)
		require.Equal(t, http.StatusOK, code)

		var n int64
		testDB.Model(&model.Order{}).Where("id = ?", pending.ID).Count(&n)
		assert.Zero(t, n)
	})
}

// TestOrderOwnership 别人的订单一律按「不存在」处理，不能泄露 ID 是否存在
func TestOrderOwnership(t *testing.T) {
	requireDB(t)
	_, tokenA := newUser(t)
	_, tokenB := newUser(t)

	order := createOrder(t, tokenA, 800)

	t.Run("查别人的订单返回 404", func(t *testing.T) {
		code, resp := do(t, http.MethodGet, fmt.Sprintf("/api/v1/orders/%d", order.ID), tokenB, nil)
		assert.Equal(t, http.StatusNotFound, code)
		assert.Equal(t, errcode.ErrOrderNotFound.Code(), resp.Code)
	})

	t.Run("改别人的订单状态返回 404 且数据不变", func(t *testing.T) {
		code, _ := do(t, http.MethodPut, fmt.Sprintf("/api/v1/orders/%d/status", order.ID), tokenB,
			model.UpdateOrderStatusRequest{Status: model.OrderStatusCancelled})
		assert.Equal(t, http.StatusNotFound, code)

		var row model.Order
		require.NoError(t, testDB.First(&row, order.ID).Error)
		assert.Equal(t, model.OrderStatusPending, row.Status)
	})

	t.Run("别人的订单不出现在自己列表里", func(t *testing.T) {
		code, resp := do(t, http.MethodGet, "/api/v1/orders", tokenB, nil)
		require.Equal(t, http.StatusOK, code)
		var list struct {
			Total int64 `json:"total"`
		}
		require.NoError(t, json.Unmarshal(resp.Data, &list))
		assert.Zero(t, list.Total)
	})
}
