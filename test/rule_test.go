package test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/internal/service"
)

// TestRuleLifecycle 验证规则系统完整生命周期：
// 建指标 → 建规则配置 → 保存规则（编译）→ 现场验证 → 发布 → 编辑(fork) → 切流 → 求值 → 全量收尾。
// 这是核心链路的集成验证，跑通意味着编译/执行/发布/灰度/eval 全链路正确。
func TestRuleLifecycle(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().Unix()

	// 1. 建项目
	proj := &model.Project{
		Name: "规则测试项目", Logo: "proj_" + suffix, Status: model.ProjectStatusActive,
		CreatedUser: "tester", UpdatedUser: "tester", CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateProject(ctx, proj); err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteProject(ctx, proj.ID) })

	// 2. 建指标（field）。
	// 解析路径必须带「项目标识」前缀（与 service.CreateField 的校验一致），
	// 因此入参也要按 {"<项目标识>": {...}} 命名空间传递（见 service.Eval 的 checkNamespace）。
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

	// ctxData 按项目命名空间包装入参，供保存 / 试跑 / 求值共用。
	ctxData := func(v any) map[string]any {
		return map[string]any{proj.Logo: map[string]any{"material": map[string]any{"vertical_type": v}}}
	}

	// 3. 建 type=rule 的配置（v1）
	cfg := &model.Config{
		ProjectID: fmt.Sprintf("%d", proj.ID), ConfigPackID: 0, Name: "审核规则", Logo: "cfg_" + suffix,
		Type: model.ConfigTypeRule, Version: "v1", Status: model.ConfigStatusPending, IsLatest: model.ConfigLatestYes,
		CreatedUser: "tester", UpdatedUser: "tester", CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateConfig(ctx, cfg); err != nil {
		t.Fatalf("建配置失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteConfig(ctx, cfg.ID) })

	// 4. 保存规则（编译 + 收集 bind_var）
	ruleSrc := fmt.Sprintf("def judge():\n  if ##%d**%s## in (0,1,5):\n    return 1\n  return 0\nresult = judge()", fld.ID, parsePath)
	resp, err := service.UpdateRuleConfig(ctx, "tester", &model.UpdateRuleConfigRequest{
		ConfigID: cfg.ID, Name: cfg.Name, Rule: ruleSrc, ResultType: model.ResultTypePassRejectReview,
		TestData: ctxData(1),
	})
	if err != nil {
		t.Fatalf("保存规则失败: %v", err)
	}
	if resp.BindVar != fmt.Sprintf("%d", fld.ID) {
		t.Errorf("bind_var 错误: %s", resp.BindVar)
	}

	// 4.1 返回值类型混用的规则必须被保存闸门拒绝
	// （强类型约定：同一函数不能既 return True 又 return 0，否则同一份规则
	//   在不同分支会产出 bool / int 两种结果类型）
	mixedSrc := fmt.Sprintf("def judge():\n  if ##%d**%s## in (0,1,5):\n    return True\n  return 0\nresult = judge()", fld.ID, parsePath)
	if _, err := service.UpdateRuleConfig(ctx, "tester", &model.UpdateRuleConfigRequest{
		ConfigID: cfg.ID, Name: cfg.Name, Rule: mixedSrc, ResultType: model.ResultTypePassRejectReview,
		TestData: ctxData(1),
	}); err == nil {
		t.Error("返回值类型混用（bool + int）的规则应被拒绝保存")
	} else if !strings.Contains(err.Error(), "结果类型") {
		t.Errorf("报错应说明结果类型不一致，实际: %v", err)
	}

	// 5. 现场验证（命中 → return 1）
	tr, err := service.TestRun(ctx, &model.TestRunRequest{
		Rule: ruleSrc, ResultType: model.ResultTypePassRejectReview,
		Data: ctxData(float64(1)),
	})
	if err != nil {
		t.Fatalf("验证失败: %v", err)
	}
	if tr.Value != int(1) {
		t.Errorf("验证结果期望 1，实际 %v", tr.Value)
	}

	// 6. 发布 v1（全量）
	if _, err := service.Publish(ctx, cfg.ID); err != nil {
		t.Fatalf("发布 v1 失败: %v", err)
	}

	// 7. 编辑生效版本 → fork 新版本 v2（status=0）
	resp2, err := service.UpdateRuleConfig(ctx, "tester", &model.UpdateRuleConfigRequest{
		ConfigID: cfg.ID, Name: cfg.Name, Rule: ruleSrc, ResultType: model.ResultTypePassRejectReview,
		TestData: ctxData(1),
	})
	if err != nil {
		t.Fatalf("编辑生效版本失败: %v", err)
	}
	if resp2.ConfigID == cfg.ID {
		t.Errorf("编辑生效版本应 fork 新版本，但 config_id 未变")
	}
	if resp2.ConfigID != 0 {
		t.Cleanup(func() { _ = data.DeleteConfig(ctx, resp2.ConfigID) })
	}

	// 8. 切流 0.5 到 v2（灰度）
	if _, err := service.CutProgress(ctx, "tester", &model.CutProgressRequest{
		ConfigID: resp2.ConfigID, CutNum: 0.5,
	}); err != nil {
		t.Fatalf("切流失败: %v", err)
	}

	// 9. 求值（灰度期，默认路径，返回结果应为 1 或 0 都合法，验证链路不报错）
	ev, err := service.Eval(ctx, &model.EvalRequest{
		Pack: proj.Logo, Key: cfg.Logo,
		Data: ctxData(float64(1)),
	})
	if err != nil {
		t.Fatalf("求值失败: %v", err)
	}
	if ev.Value != int(1) && ev.Value != int(0) {
		t.Errorf("求值结果异常: %v", ev.Value)
	}

	// 10. 全量发布 v2（灰度收尾，老版本下线 + 清灰度）
	if _, err := service.Publish(ctx, resp2.ConfigID); err != nil {
		t.Fatalf("发布 v2 失败: %v", err)
	}

	// 11. 切流比例越界校验
	if _, err := service.CutProgress(ctx, "tester", &model.CutProgressRequest{
		ConfigID: resp2.ConfigID, CutNum: 5,
	}); err == nil {
		t.Error("切流比例越界应报错")
	}
}

// TestSaveRuleTypeGuard 验证对非规则类型 config 保存规则会被拒绝
// （否则保存了规则但 eval 永不执行，静默失败）。
func TestSaveRuleTypeGuard(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().Unix()

	proj := &model.Project{
		Name: "类型守卫测试", Logo: "proj_g_" + suffix, Status: model.ProjectStatusActive,
		CreatedUser: "tester", UpdatedUser: "tester", CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateProject(ctx, proj); err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteProject(ctx, proj.ID) })

	// 非 rule 类型的 config
	cfg := &model.Config{
		ProjectID: fmt.Sprintf("%d", proj.ID), Name: "非规则", Logo: "cfg_g_" + suffix,
		Type: "json", Version: "v1", Status: model.ConfigStatusPending, IsLatest: model.ConfigLatestYes,
		CreatedUser: "tester", UpdatedUser: "tester", CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateConfig(ctx, cfg); err != nil {
		t.Fatalf("建配置失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteConfig(ctx, cfg.ID) })

	if _, err := service.UpdateRuleConfig(ctx, "tester", &model.UpdateRuleConfigRequest{
		ConfigID: cfg.ID, Name: cfg.Name, Rule: "result = 1", ResultType: model.ResultTypePassRejectReview,
	}); err == nil {
		t.Error("对非规则类型 config 保存规则应被拒绝")
	}
}

// TestGetRuleEmptyWhenNotSaved 验证 type=rule 的 config 未保存规则时，
// GetRule 返回空响应（允许前端首次打开编辑器保存）而非 404。
func TestGetRuleEmptyWhenNotSaved(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().Unix()

	proj := &model.Project{
		Name: "空规则测试", Logo: "proj_e_" + suffix, Status: model.ProjectStatusActive,
		CreatedUser: "tester", UpdatedUser: "tester", CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateProject(ctx, proj); err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteProject(ctx, proj.ID) })

	cfg := &model.Config{
		ProjectID: fmt.Sprintf("%d", proj.ID), Name: "空规则", Logo: "cfg_e_" + suffix,
		Type: model.ConfigTypeRule, Version: "v1", Status: model.ConfigStatusPending, IsLatest: model.ConfigLatestYes,
		CreatedUser: "tester", UpdatedUser: "tester", CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateConfig(ctx, cfg); err != nil {
		t.Fatalf("建配置失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteConfig(ctx, cfg.ID) })

	resp, err := service.GetRule(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("未保存规则时 GetRule 应返回空响应而非错误: %v", err)
	}
	if resp.RuleSource != "" || resp.ResultType != model.ResultTypePassRejectReview {
		t.Errorf("空规则响应字段错误: source=%q result_type=%q", resp.RuleSource, resp.ResultType)
	}
}

// TestCreateRuleConfig 验证「一步建规则」：创建 type=rule 配置 + 规则内容，
// version 自动生成；同 logo 二次创建被拒绝（同标识后续版本只能走 fork）。
func TestCreateRuleConfig(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().Unix()

	proj := &model.Project{
		Name: "新增规则测试", Logo: "proj_n_" + suffix, Status: model.ProjectStatusActive,
		CreatedUser: "tester", UpdatedUser: "tester", CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateProject(ctx, proj); err != nil {
		t.Fatalf("建项目失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteProject(ctx, proj.ID) })

	cp := &model.ConfigPack{
		ProjectID: fmt.Sprintf("%d", proj.ID), Name: "新增包", Logo: "pack_n_" + suffix,
		Status: model.ConfigStatusActive, CreatedUser: "tester", UpdatedUser: "tester",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := data.CreateConfigPack(ctx, cp); err != nil {
		t.Fatalf("建配置包失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteConfigPack(ctx, cp.ID) })

	// 解析路径带「项目标识」前缀，入参按 {"<项目标识>": {...}} 命名空间传递，与领域规则一致。
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

	ctxData := func(v any) map[string]any {
		return map[string]any{proj.Logo: map[string]any{"material": map[string]any{"vertical_type": v}}}
	}

	logo := "cfg_n_" + suffix
	ruleSrc := fmt.Sprintf("def judge():\n  if ##%d**%s## in (0,1,5):\n    return 1\n  return 0\nresult = judge()", fld.ID, parsePath)
	resp, err := service.CreateRuleConfig(ctx, "tester", &model.CreateRuleConfigRequest{
		ProjectID:    fmt.Sprintf("%d", proj.ID),
		ConfigPackID: cp.ID,
		Name:         "新规则",
		Logo:         logo,
		Rule:         ruleSrc,
		ResultType:   model.ResultTypePassRejectReview,
		TestData:     ctxData(1),
	})
	if err != nil {
		t.Fatalf("一步建规则失败: %v", err)
	}
	t.Cleanup(func() { _ = data.DeleteConfig(ctx, resp.ConfigID) })
	if resp.Version == "" {
		t.Error("version 应由后端自动生成")
	}
	if resp.BindVar != fmt.Sprintf("%d", fld.ID) {
		t.Errorf("bind_var 收集错误: %s", resp.BindVar)
	}

	// 同 logo 二次创建应拒绝
	if _, err := service.CreateRuleConfig(ctx, "tester", &model.CreateRuleConfigRequest{
		ProjectID:    fmt.Sprintf("%d", proj.ID),
		ConfigPackID: cp.ID,
		Name:         "重复规则",
		Logo:         logo,
		Rule:         "result = 0",
		ResultType:   model.ResultTypePassRejectReview,
		TestData:     ctxData(1),
	}); err == nil {
		t.Error("同 logo 二次创建应被拒绝（同一标识后续版本走 fork）")
	}
}
