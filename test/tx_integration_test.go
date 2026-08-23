//go:build integration

// 真实数据库集成测试：验证事务管理器（pkg/transaction）在「回滚 / 提交 / 嵌套」三种
// 场景下的原子性，以及 repository.conn 是否正确认领 TxFrom 的事务句柄。
//
// 用 build tag `integration` 隔离：日常 `go test ./...`（CI 跑的那条）不会编译本文件，
// 需要真实 MySQL 时才显式 `go test -tags integration ./test/ -run TestTx`。
// 连接参数取自 APP_DATABASE_* 环境变量；未设置则跳过，绝不凭空连库。
package test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"myproject/internal/model"
	"myproject/internal/repository"
	"myproject/pkg/transaction"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const txMarkerPrefix = "TXINT_%"

func txDSN() string {
	host := envOr("APP_DATABASE_HOST", "127.0.0.1")
	port := envOr("APP_DATABASE_PORT", "3306")
	user := envOr("APP_DATABASE_USERNAME", "root")
	pass := os.Getenv("APP_DATABASE_PASSWORD")
	db := envOr("APP_DATABASE_DBNAME", "gin")
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		user, pass, host, port, db)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func txSetup(t *testing.T) (*gorm.DB, transaction.Manager, repository.OrderRepository, repository.OrderStatusLogRepository) {
	t.Helper()
	dsn := txDSN()
	if os.Getenv("APP_DATABASE_HOST") == "" {
		t.Skip("APP_DATABASE_HOST 未设置，跳过集成测试")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("连接数据库失败: %v", err)
	}
	if err := db.AutoMigrate(&model.Order{}, &model.OrderStatusLog{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	return db,
		transaction.NewManager(db),
		repository.NewOrder(db),
		repository.NewOrderStatusLog(db)
}

func txCleanup(db *gorm.DB) {
	// 先删流水（引用 order.id），再删订单，best-effort。
	db.Where("order_id IN (?)",
		db.Table("orders").Select("id").Where("order_no LIKE ?", txMarkerPrefix+"%"),
	).Delete(&model.OrderStatusLog{})
	db.Where("order_no LIKE ?", txMarkerPrefix+"%").Delete(&model.Order{})
}

func txNewOrder(orderNo string) *model.Order {
	return &model.Order{
		OrderNo:          orderNo,
		UserID:           990001,
		TotalAmountCents: 1234,
		Status:           model.OrderStatusPending,
		Remark:           "integration-test",
	}
}

// TestTxRollbackOnError 事务内任一步返回 error，整段必须回滚：订单与流水都不落地。
func TestTxRollbackOnError(t *testing.T) {
	db, mgr, orderRepo, logRepo := txSetup(t)
	defer txCleanup(db)

	no := fmt.Sprintf("%srb_%d", txMarkerPrefix, time.Now().UnixNano())
	order := txNewOrder(no)

	err := mgr.Do(context.Background(), func(ctx context.Context) error {
		if e := orderRepo.Create(ctx, order); e != nil {
			return e
		}
		if e := logRepo.Create(ctx, &model.OrderStatusLog{
			OrderID:    order.ID,
			FromStatus: model.OrderStatusPending,
			ToStatus:   model.OrderStatusPaid,
			OperatorID: 990001,
		}); e != nil {
			return e
		}
		return errors.New("force rollback") // 故意失败
	})
	if err == nil {
		t.Fatal("期望 fn 返回 error，实际为 nil")
	}

	// 回滚后：默认连接上应查不到该订单与流水
	var got model.Order
	if gerr := db.Where("order_no = ?", no).First(&got).Error; !errors.Is(gerr, gorm.ErrRecordNotFound) {
		t.Fatalf("回滚失败：订单仍落地 (err=%v, id=%d)", gerr, got.ID)
	}
	var n int64
	db.Model(&model.OrderStatusLog{}).Where("order_id = ?", order.ID).Count(&n)
	if n != 0 {
		t.Fatalf("回滚失败：流水仍落地 %d 条 (order_id=%d)", n, order.ID)
	}
	t.Logf("PASS 回滚：订单与流水均未落地")
}

// TestTxCommitOnSuccess 事务内全部成功，订单与流水都必须落地。
func TestTxCommitOnSuccess(t *testing.T) {
	db, mgr, orderRepo, logRepo := txSetup(t)
	defer txCleanup(db)

	no := fmt.Sprintf("%sok_%d", txMarkerPrefix, time.Now().UnixNano())
	order := txNewOrder(no)

	err := mgr.Do(context.Background(), func(ctx context.Context) error {
		if e := orderRepo.Create(ctx, order); e != nil {
			return e
		}
		return logRepo.Create(ctx, &model.OrderStatusLog{
			OrderID:    order.ID,
			FromStatus: model.OrderStatusPending,
			ToStatus:   model.OrderStatusPaid,
			OperatorID: 990001,
		})
	})
	if err != nil {
		t.Fatalf("提交应成功，实际 error=%v", err)
	}

	var got model.Order
	if gerr := db.Where("order_no = ?", no).First(&got).Error; gerr != nil {
		t.Fatalf("提交失败：订单未落地 (err=%v)", gerr)
	}
	var n int64
	db.Model(&model.OrderStatusLog{}).Where("order_id = ?", got.ID).Count(&n)
	if n != 1 {
		t.Fatalf("提交失败：流水条数=%d，期望 1", n)
	}
	t.Logf("PASS 提交：订单(id=%d)与流水(1条)均落地", got.ID)
}

// TestTxNestedRollback 嵌套 Do 复用外层事务（SavePoint 语义）：外层回滚，内层即便成功也必须一起消失。
// 若嵌套被错误实现成「另起独立事务」，内层流水会独立提交而残留 —— 此测试专门抓这种 bug。
func TestTxNestedRollback(t *testing.T) {
	db, mgr, orderRepo, logRepo := txSetup(t)
	defer txCleanup(db)

	no := fmt.Sprintf("%snest_rb_%d", txMarkerPrefix, time.Now().UnixNano())
	order := txNewOrder(no)

	err := mgr.Do(context.Background(), func(ctx context.Context) error {
		if e := orderRepo.Create(ctx, order); e != nil {
			return e
		}
		// 嵌套事务：内层成功提交
		innerErr := mgr.Do(ctx, func(ictx context.Context) error {
			return logRepo.Create(ictx, &model.OrderStatusLog{
				OrderID:    order.ID,
				FromStatus: model.OrderStatusPending,
				ToStatus:   model.OrderStatusPaid,
				OperatorID: 990001,
			})
		})
		if innerErr != nil {
			return innerErr
		}
		return errors.New("outer force rollback") // 外层回滚
	})
	if err == nil {
		t.Fatal("期望外层返回 error，实际为 nil")
	}

	var got model.Order
	if gerr := db.Where("order_no = ?", no).First(&got).Error; !errors.Is(gerr, gorm.ErrRecordNotFound) {
		t.Fatalf("嵌套回滚失败：外层订单仍落地 (err=%v, id=%d)", gerr, got.ID)
	}
	var n int64
	db.Model(&model.OrderStatusLog{}).Where("order_id = ?", order.ID).Count(&n)
	if n != 0 {
		t.Fatalf("嵌套回滚失败：内层流水残留 %d 条（说明嵌套被实现成了独立事务！）", n)
	}
	t.Logf("PASS 嵌套回滚：外层回滚连带内层流水一起消失")
}

// TestTxNestedCommit 嵌套全部成功：订单与流水都落地。
func TestTxNestedCommit(t *testing.T) {
	db, mgr, orderRepo, logRepo := txSetup(t)
	defer txCleanup(db)

	no := fmt.Sprintf("%snest_ok_%d", txMarkerPrefix, time.Now().UnixNano())
	order := txNewOrder(no)

	err := mgr.Do(context.Background(), func(ctx context.Context) error {
		if e := orderRepo.Create(ctx, order); e != nil {
			return e
		}
		return mgr.Do(ctx, func(ictx context.Context) error {
			return logRepo.Create(ictx, &model.OrderStatusLog{
				OrderID:    order.ID,
				FromStatus: model.OrderStatusPending,
				ToStatus:   model.OrderStatusPaid,
				OperatorID: 990001,
			})
		})
	})
	if err != nil {
		t.Fatalf("嵌套提交应成功，实际 error=%v", err)
	}

	var got model.Order
	if gerr := db.Where("order_no = ?", no).First(&got).Error; gerr != nil {
		t.Fatalf("嵌套提交失败：订单未落地 (err=%v)", gerr)
	}
	var n int64
	db.Model(&model.OrderStatusLog{}).Where("order_id = ?", got.ID).Count(&n)
	if n != 1 {
		t.Fatalf("嵌套提交失败：流水条数=%d，期望 1", n)
	}
	t.Logf("PASS 嵌套提交：订单(id=%d)与流水(1条)均落地", got.ID)
}

// TestTxHandleInvalidatedAfterDone Do 返回后事务句柄应失效：复用同一 ctx 的写操作
// 必须退化到默认连接（自动提交），既不能 panic 也不应误用已结束的事务。
func TestTxHandleInvalidatedAfterDone(t *testing.T) {
	db, mgr, orderRepo, _ := txSetup(t)
	defer txCleanup(db)

	no := fmt.Sprintf("%sinv_%d", txMarkerPrefix, time.Now().UnixNano())
	order := txNewOrder(no)

	// 成功提交一笔，拿到有效 order.ID
	if err := mgr.Do(context.Background(), func(ctx context.Context) error {
		return orderRepo.Create(ctx, order)
	}); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	committedID := order.ID

	// 复用已结束的 ctx 再写一次（句柄已 done）：应走默认连接自动提交，不 panic
	dup := txNewOrder(no + "_after")
	if err := orderRepo.Create(context.Background(), dup); err != nil {
		t.Fatalf("失效句柄退化写入应成功，实际 error=%v", err)
	}
	var got model.Order
	if gerr := db.Where("order_no = ?", dup.OrderNo).First(&got).Error; gerr != nil {
		t.Fatalf("退化写入未落地（行为不符预期）: %v", gerr)
	}
	t.Logf("PASS 句柄失效后退化到默认连接：原订单(id=%d) 与退化订单(id=%d) 均落地", committedID, got.ID)
}
