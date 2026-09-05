package test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"myproject/internal/model"
	"myproject/internal/resource"
	"myproject/pkg/transaction"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// txOrder 造一个带标记的订单，便于清理
func txOrder(t *testing.T) *model.Order {
	t.Helper()
	no := fmt.Sprintf("TXIT%d", time.Now().UnixNano())
	order := &model.Order{
		OrderNo:          no,
		UserID:           990001,
		TotalAmountCents: 1234,
		Status:           model.OrderStatusPending,
	}
	t.Cleanup(func() {
		testDB.Where("order_id = ?", order.ID).Delete(&model.OrderStatusLog{})
		testDB.Where("order_no = ?", no).Delete(&model.Order{})
	})
	return order
}

func countLogs(t *testing.T, orderID uint64) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Model(&model.OrderStatusLog{}).Where("order_id = ?", orderID).Count(&n).Error)
	return n
}

// TestTxRollback 闭包返回 error 时两次写入都不落地
func TestTxRollback(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	order := txOrder(t)

	err := transaction.Do(ctx, func(ctx context.Context) error {
		if err := resource.DB(ctx).Create(order).Error; err != nil {
			return err
		}
		if err := resource.DB(ctx).Create(&model.OrderStatusLog{
			OrderID: order.ID, FromStatus: 0, ToStatus: 1, OperatorID: 1,
		}).Error; err != nil {
			return err
		}
		return errors.New("boom")
	})
	require.Error(t, err)

	var n int64
	testDB.Model(&model.Order{}).Where("order_no = ?", order.OrderNo).Count(&n)
	assert.Zero(t, n, "订单应被回滚")
	assert.Zero(t, countLogs(t, order.ID), "流水应被回滚")
}

// TestTxCommit 闭包返回 nil 时两次写入都落地
func TestTxCommit(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	order := txOrder(t)

	err := transaction.Do(ctx, func(ctx context.Context) error {
		if err := resource.DB(ctx).Create(order).Error; err != nil {
			return err
		}
		return resource.DB(ctx).Create(&model.OrderStatusLog{
			OrderID: order.ID, FromStatus: 0, ToStatus: 1, OperatorID: 1,
		}).Error
	})
	require.NoError(t, err)

	var n int64
	testDB.Model(&model.Order{}).Where("order_no = ?", order.OrderNo).Count(&n)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, int64(1), countLogs(t, order.ID))
}

// TestTxNested 嵌套 Do 复用外层事务（SavePoint 语义），不是另起一个独立事务。
// 判定方法：内层成功提交、外层回滚 —— 独立事务的话内层写入会留在库里。
func TestTxNested(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	t.Run("内层提交外层回滚则全部消失", func(t *testing.T) {
		order := txOrder(t)
		err := transaction.Do(ctx, func(ctx context.Context) error {
			if err := resource.DB(ctx).Create(order).Error; err != nil {
				return err
			}
			inner := transaction.Do(ctx, func(ctx context.Context) error {
				return resource.DB(ctx).Create(&model.OrderStatusLog{
					OrderID: order.ID, FromStatus: 0, ToStatus: 1, OperatorID: 1,
				}).Error
			})
			require.NoError(t, inner)
			return errors.New("outer rollback")
		})
		require.Error(t, err)

		var n int64
		testDB.Model(&model.Order{}).Where("order_no = ?", order.OrderNo).Count(&n)
		assert.Zero(t, n)
		assert.Zero(t, countLogs(t, order.ID), "内层写入必须随外层一起回滚")
	})

	t.Run("内层回滚不影响外层提交", func(t *testing.T) {
		order := txOrder(t)
		err := transaction.Do(ctx, func(ctx context.Context) error {
			if err := resource.DB(ctx).Create(order).Error; err != nil {
				return err
			}
			inner := transaction.Do(ctx, func(ctx context.Context) error {
				if err := resource.DB(ctx).Create(&model.OrderStatusLog{
					OrderID: order.ID, FromStatus: 0, ToStatus: 9, OperatorID: 1,
				}).Error; err != nil {
					return err
				}
				return errors.New("inner rollback")
			})
			require.Error(t, inner)
			// 内层回滚后外层仍可继续写
			return resource.DB(ctx).Create(&model.OrderStatusLog{
				OrderID: order.ID, FromStatus: 0, ToStatus: 1, OperatorID: 2,
			}).Error
		})
		require.NoError(t, err)

		var n int64
		testDB.Model(&model.Order{}).Where("order_no = ?", order.OrderNo).Count(&n)
		assert.Equal(t, int64(1), n, "外层写入应提交")
		assert.Equal(t, int64(1), countLogs(t, order.ID), "只应留下外层那一条流水")
	})
}

// TestTxHandleInvalidAfterDone Do 返回后 ctx 里的事务句柄必须失效，
// 否则闭包把 ctx 交给后台 goroutine 就会操作已 Commit 的 *sql.Tx。
func TestTxHandleInvalidAfterDone(t *testing.T) {
	requireDB(t)

	var escaped context.Context
	err := transaction.Do(context.Background(), func(ctx context.Context) error {
		escaped = ctx
		_, ok := transaction.TxFrom(ctx)
		assert.True(t, ok, "闭包内应能取到事务句柄")
		return errors.New("rollback")
	})
	require.Error(t, err)

	_, ok := transaction.TxFrom(escaped)
	assert.False(t, ok, "Do 返回后句柄必须失效")
}
