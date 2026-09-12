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

// txProject 造一个带标记的项目，便于清理
func txProject(t *testing.T) *model.Project {
	t.Helper()
	logo := fmt.Sprintf("TXIT%d", time.Now().UnixNano())
	p := &model.Project{
		Name:        "tx",
		Logo:        logo,
		Status:      1,
		CreatedUser: "t",
		UpdatedUser: "t",
	}
	t.Cleanup(func() {
		testDB.Where("logo = ?", logo).Delete(&model.Project{})
	})
	return p
}

func countProject(t *testing.T, logo string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Model(&model.Project{}).Where("logo = ?", logo).Count(&n).Error)
	return n
}

// TestTxRollback 闭包返回 error 时两次写入都不落地
func TestTxRollback(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	p := txProject(t)

	err := transaction.Do(ctx, func(ctx context.Context) error {
		if err := resource.DB(ctx).Create(p).Error; err != nil {
			return err
		}
		if err := resource.DB(ctx).Create(&model.Project{
			Name: "tx2", Logo: p.Logo + "x", Status: 1, CreatedUser: "t", UpdatedUser: "t",
		}).Error; err != nil {
			return err
		}
		return errors.New("boom")
	})
	require.Error(t, err)

	assert.Zero(t, countProject(t, p.Logo), "项目应被回滚")
	assert.Zero(t, countProject(t, p.Logo+"x"), "内层写入应被回滚")
}

// TestTxCommit 闭包返回 nil 时两次写入都落地
func TestTxCommit(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	p := txProject(t)

	err := transaction.Do(ctx, func(ctx context.Context) error {
		if err := resource.DB(ctx).Create(p).Error; err != nil {
			return err
		}
		return resource.DB(ctx).Create(&model.Project{
			Name: "tx2", Logo: p.Logo + "x", Status: 1, CreatedUser: "t", UpdatedUser: "t",
		}).Error
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), countProject(t, p.Logo))
	assert.Equal(t, int64(1), countProject(t, p.Logo+"x"))
}

// TestTxNested 嵌套 Do 复用外层事务（SavePoint 语义），不是另起一个独立事务。
// 判定方法：内层成功提交、外层回滚 —— 独立事务的话内层写入会留在库里。
func TestTxNested(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	t.Run("内层提交外层回滚则全部消失", func(t *testing.T) {
		p := txProject(t)
		err := transaction.Do(ctx, func(ctx context.Context) error {
			if err := resource.DB(ctx).Create(p).Error; err != nil {
				return err
			}
			inner := transaction.Do(ctx, func(ctx context.Context) error {
				return resource.DB(ctx).Create(&model.Project{
					Name: "tx2", Logo: p.Logo + "x", Status: 1, CreatedUser: "t", UpdatedUser: "t",
				}).Error
			})
			require.NoError(t, inner)
			return errors.New("outer rollback")
		})
		require.Error(t, err)

		assert.Zero(t, countProject(t, p.Logo))
		assert.Zero(t, countProject(t, p.Logo+"x"), "内层写入必须随外层一起回滚")
	})

	t.Run("内层回滚不影响外层提交", func(t *testing.T) {
		p := txProject(t)
		err := transaction.Do(ctx, func(ctx context.Context) error {
			if err := resource.DB(ctx).Create(p).Error; err != nil {
				return err
			}
			inner := transaction.Do(ctx, func(ctx context.Context) error {
				if err := resource.DB(ctx).Create(&model.Project{
					Name: "tx2", Logo: p.Logo + "x", Status: 1, CreatedUser: "t", UpdatedUser: "t",
				}).Error; err != nil {
					return err
				}
				return errors.New("inner rollback")
			})
			require.Error(t, inner)
			// 内层回滚后外层仍可继续写
			return resource.DB(ctx).Create(&model.Project{
				Name: "tx3", Logo: p.Logo + "y", Status: 1, CreatedUser: "t", UpdatedUser: "t",
			}).Error
		})
		require.NoError(t, err)

		assert.Equal(t, int64(1), countProject(t, p.Logo), "外层写入应提交")
		assert.Equal(t, int64(1), countProject(t, p.Logo+"y"), "只应留下外层那一次写入")
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
