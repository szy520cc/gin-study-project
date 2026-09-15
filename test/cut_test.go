package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/internal/service"
)

// TestCutCancelClearsFieldsAndIdempotent 验证切流/取消切流：
//  1. 灰度标记写在「线上版本」行上（cut_num/cut_version/cut_by）；
//  2. 取消切流（cut_num=0）清空全部切流字段，不留操作者/时间残留；
//  3. 重复取消幂等；无生效版本时取消也幂等成功（不再报「无生效版本」）。
func TestCutCancelClearsFieldsAndIdempotent(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().Unix()

	proj := &model.Project{
		Name: "切流取消测试", Logo: "proj_c_" + suffix, Status: model.ProjectStatusActive,
		CreatedUser: "tester", UpdatedUser: "tester", CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateProject(ctx, proj); err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteProject(ctx, proj.ID) })

	parsePath := proj.Logo + ".material.vertical_type"
	fld := &model.Field{
		ProjectID: fmt.Sprintf("%d", proj.ID), Name: "资源类型", Type: model.FieldTypeInt,
		DefaultValue: `{"type":"int","value":-1}`, ParsePath: parsePath,
		Status: model.FieldStatusActive, CreatedUser: "tester", UpdatedUser: "tester",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateField(ctx, fld); err != nil {
		t.Fatalf("建字段失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteField(ctx, fld.ID) })

	ruleSrc := fmt.Sprintf("def judge():\n  if ##%d**%s## in (0,1,5):\n    return 1\n  return 0\nresult = judge()", fld.ID, parsePath)
	ctxData := func(v any) map[string]any {
		return map[string]any{proj.Logo: map[string]any{"material": map[string]any{"vertical_type": v}}}
	}

	// v1（待审核）→ 发布成线上版本
	v1, err := service.CreateRuleConfig(ctx, "tester", &model.CreateRuleConfigRequest{
		ProjectID: fmt.Sprintf("%d", proj.ID), ConfigPackID: 0, Name: "切流规则",
		Logo: "cfg_c_" + suffix, Rule: ruleSrc, ResultType: model.ResultTypePassRejectReview,
		TestData: ctxData(1),
	})
	if err != nil {
		t.Fatalf("建规则失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteConfig(ctx, v1.ConfigID) })

	if _, err := service.Publish(ctx, v1.ConfigID); err != nil {
		t.Fatalf("发布 v1 失败: %v", err)
	}

	// 编辑线上版本 → fork 出 v2（待审核）
	v2, err := service.SaveRule(ctx, "tester", &model.SaveRuleRequest{
		ConfigID: v1.ConfigID, Rule: ruleSrc, ResultType: model.ResultTypePassRejectReview,
		TestData: ctxData(1),
	})
	if err != nil {
		t.Fatalf("fork v2 失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteConfig(ctx, v2.ConfigID) })
	if v2.ConfigID == v1.ConfigID {
		t.Fatalf("编辑线上版本应 fork 新版本")
	}

	// 切流 50% 到 v2
	if _, err := service.CutProgress(ctx, "tester", &model.CutProgressRequest{
		ConfigID: v2.ConfigID, CutNum: 0.5,
	}); err != nil {
		t.Fatalf("切流失败: %v", err)
	}

	active, err := data.GetConfigByID(ctx, v1.ConfigID)
	if err != nil {
		t.Fatalf("读线上版本失败: %v", err)
	}
	if active.CutNum != 0.5 || active.CutVersion != v2.Version || active.CutBy != "tester" {
		t.Fatalf("切流标记应挂在线上版本行: cut_num=%v cut_version=%q cut_by=%q",
			active.CutNum, active.CutVersion, active.CutBy)
	}

	// 取消切流 → 清空全部切流字段
	if _, err := service.CutProgress(ctx, "tester", &model.CutProgressRequest{
		ConfigID: v2.ConfigID, CutNum: 0,
	}); err != nil {
		t.Fatalf("取消切流失败: %v", err)
	}

	active2, err := data.GetConfigByID(ctx, v1.ConfigID)
	if err != nil {
		t.Fatalf("读线上版本失败: %v", err)
	}
	if active2.CutNum != 0 || active2.CutVersion != "" || active2.CutBy != "" || active2.CutAt != 0 {
		t.Errorf("取消切流应清空全部切流字段，实际 cut_num=%v cut_version=%q cut_by=%q cut_at=%d",
			active2.CutNum, active2.CutVersion, active2.CutBy, active2.CutAt)
	}

	// 重复取消：幂等
	if _, err := service.CutProgress(ctx, "tester", &model.CutProgressRequest{
		ConfigID: v2.ConfigID, CutNum: 0,
	}); err != nil {
		t.Errorf("重复取消切流应幂等成功，实际: %v", err)
	}

	// 无生效版本时取消：幂等成功（新建一个只有待审核版本的 logo）
	vX, err := service.CreateRuleConfig(ctx, "tester", &model.CreateRuleConfigRequest{
		ProjectID: fmt.Sprintf("%d", proj.ID), ConfigPackID: 0, Name: "无线上版本",
		Logo: "cfg_nc_" + suffix, Rule: ruleSrc, ResultType: model.ResultTypePassRejectReview,
		TestData: ctxData(1),
	})
	if err != nil {
		t.Fatalf("建规则失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteConfig(ctx, vX.ConfigID) })

	if _, err := service.CutProgress(ctx, "tester", &model.CutProgressRequest{
		ConfigID: vX.ConfigID, CutNum: 0,
	}); err != nil {
		t.Errorf("无生效版本时取消切流应幂等成功，实际: %v", err)
	}
}
