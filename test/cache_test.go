package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/internal/resource"
	"myproject/internal/service"
)

// 本文件用「真实 Redis」断言缓存行为（快照投放 / 失效 / 指针清理）。
// 之前 test/setup 传的是 nil Redis，SetSnapshot/DelSnapshot 全是 no-op，
// 等于这些行为没有任何断言 —— 所以这里接真 Redis，连不上时 requireRedis 跳过而不是变绿。

// cacheGrayName 缓存用例里「线上版本」的配置名。
// 用于 fork 时回填 name（UpdateRuleConfig 需要 name，且它会把 name 写到 fork 出的新版本）；
// 用例不断言名称，这里保持一致只为可读性。
const cacheGrayName = "线上规则"

// cacheFixture 缓存用例的公共装置：项目 + 字段（解析路径带项目标识前缀）。
type cacheFixture struct {
	proj      *model.Project
	fld       *model.Field
	parsePath string
}

func (f *cacheFixture) data(v any) map[string]any {
	return map[string]any{f.proj.Logo: map[string]any{"material": map[string]any{"vertical_type": v}}}
}

func (f *cacheFixture) rule() string {
	return fmt.Sprintf("def judge():\n  if ##%d**%s## in (0,1,5):\n    return 1\n  return 0\nresult = judge()", f.fld.ID, f.parsePath)
}

// newCacheFixture 建项目 + 字段，并注册 DB 与 Redis 的清理。
func newCacheFixture(t *testing.T) *cacheFixture {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().Unix()

	proj := &model.Project{
		Name: "缓存测试项目", Logo: "proj_cache_" + suffix, Status: model.ProjectStatusActive,
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

	// 清理该 logo 在 Redis 上的全部 key（cur_ver / eval_key / latest）。
	// logo 带纳秒后缀，pattern 不会误伤其它用例的数据。
	logo := proj.Logo
	t.Cleanup(func() {
		rc := resource.Redis()
		if rc == nil {
			return
		}
		keys, err := rc.Client().Keys(ctx, "*"+logo+"*").Result()
		if err != nil || len(keys) == 0 {
			return
		}
		_ = rc.Client().Del(ctx, keys...).Err()
	})

	return &cacheFixture{proj: proj, fld: fld, parsePath: parsePath}
}

// createDraft 建一个待审核（status=0）的规则配置。
func (f *cacheFixture) createDraft(t *testing.T, name, logoPrefix string) *model.RuleResponse {
	t.Helper()
	ctx := context.Background()
	resp, err := service.CreateRuleConfig(ctx, "tester", &model.CreateRuleConfigRequest{
		ProjectID: fmt.Sprintf("%d", f.proj.ID), ConfigPackID: 0, Name: name,
		Logo: logoPrefix + fmt.Sprintf("%d", time.Now().UnixNano()), Rule: f.rule(),
		ResultType: model.ResultTypePassRejectReview, TestData: f.data(1),
	})
	if err != nil {
		t.Fatalf("建规则失败: %v", err)
	}
	id := resp.ConfigID
	t.Cleanup(func() { _ = data.DeleteConfig(context.Background(), id) })
	return resp
}

// forkDraft 编辑生效版本 → fork 出新草稿（不可变发布链）。
func (f *cacheFixture) forkDraft(t *testing.T, configID uint64) *model.RuleResponse {
	t.Helper()
	resp, err := service.UpdateRuleConfig(context.Background(), "tester", &model.UpdateRuleConfigRequest{
		ConfigID: configID, Name: cacheGrayName, Rule: f.rule(), ResultType: model.ResultTypePassRejectReview,
		TestData: f.data(1),
	})
	if err != nil {
		t.Fatalf("fork 新版本失败: %v", err)
	}
	id := resp.ConfigID
	t.Cleanup(func() { _ = data.DeleteConfig(context.Background(), id) })
	return resp
}

// seedSnapshot 预置一份「陈旧」快照（只有标识、没有规则内容），
// 用于模拟"历史上被显式 version 求值写入过"的缓存。
func seedSnapshot(t *testing.T, pack, logo, version string) {
	t.Helper()
	snap := &model.ConfigSnapshot{Pack: pack, Extension: logo, Version: version, Type: model.ConfigTypeRule}
	if err := data.SetSnapshot(context.Background(), pack, logo, version, snap); err != nil {
		t.Fatalf("预置版本快照失败: %v", err)
	}
}

// seedLatestSnapshot 预置一份草稿(latest)快照。
func seedLatestSnapshot(t *testing.T, pack, logo string) {
	t.Helper()
	snap := &model.ConfigSnapshot{Pack: pack, Extension: logo, Type: model.ConfigTypeRule}
	if err := data.SetLatestSnapshot(context.Background(), pack, logo, snap); err != nil {
		t.Fatalf("预置草稿快照失败: %v", err)
	}
}

// TestPublishRefreshesSnapshotAndClearsLatest 发布后：
//   - 新上线版本的快照被「重建」（带规则、无灰度），而不是沿用历史陈旧内容；
//   - 草稿(latest)快照被清掉；版本指针指向新版本。
func TestPublishRefreshesSnapshotAndClearsLatest(t *testing.T) {
	requireRedis(t)
	ctx := context.Background()
	f := newCacheFixture(t)

	v1 := f.createDraft(t, "发布规则", "cfg_cache_pub_")
	seedSnapshot(t, f.proj.Logo, v1.Logo, v1.Version)
	seedLatestSnapshot(t, f.proj.Logo, v1.Logo)

	if _, err := service.Publish(ctx, v1.ConfigID); err != nil {
		t.Fatalf("发布失败: %v", err)
	}

	snap := data.GetSnapshot(ctx, f.proj.Logo, v1.Logo, v1.Version)
	if snap == nil {
		t.Fatal("发布后应刷新出新版本快照")
	}
	if snap.Rule == nil || snap.Rule.Script == "" {
		t.Error("发布后的快照应带已编译规则（证明是重建，而非沿用陈旧快照）")
	}
	if snap.CutNum != 0 || snap.NewVersion != nil {
		t.Errorf("发布后快照不应带灰度：cut_num=%v new_version=%v", snap.CutNum, snap.NewVersion != nil)
	}
	if data.GetLatestSnapshot(ctx, f.proj.Logo, v1.Logo) != nil {
		t.Error("发布后应清掉草稿(latest)快照")
	}
	if got := data.GetCurVer(ctx, f.proj.Logo, v1.Logo); got != v1.Version {
		t.Errorf("发布后指针应指向新版本：want %q got %q", v1.Version, got)
	}
}

// TestEditDraftInvalidatesVersionedSnapshot 原地编辑草稿 → 删掉该版本的版本化快照。
func TestEditDraftInvalidatesVersionedSnapshot(t *testing.T) {
	requireRedis(t)
	ctx := context.Background()
	f := newCacheFixture(t)

	v1 := f.createDraft(t, "草稿规则", "cfg_cache_edit_")
	seedSnapshot(t, f.proj.Logo, v1.Logo, v1.Version)
	if data.GetSnapshot(ctx, f.proj.Logo, v1.Logo, v1.Version) == nil {
		t.Fatal("前置失败：预置快照未写入")
	}

	if _, err := service.UpdateRuleConfig(ctx, "tester", &model.UpdateRuleConfigRequest{
		ConfigID: v1.ConfigID, Name: v1.Name, Rule: f.rule(), ResultType: model.ResultTypePassRejectReview,
		TestData: f.data(1),
	}); err != nil {
		t.Fatalf("原地编辑草稿失败: %v", err)
	}

	if data.GetSnapshot(ctx, f.proj.Logo, v1.Logo, v1.Version) != nil {
		t.Error("原地编辑草稿后应删掉该版本快照（否则发布后会命中旧规则）")
	}
}

// TestCutSnapshotCarriesGrayAndCancelClearsIt 切流快照带灰度（NewVersion 指向待上线版本）；
// 取消切流后同一 key 被重投为「无灰度」。
func TestCutSnapshotCarriesGrayAndCancelClearsIt(t *testing.T) {
	requireRedis(t)
	ctx := context.Background()
	f := newCacheFixture(t)

	v1 := f.createDraft(t, "线上规则", "cfg_cache_cut_")
	if _, err := service.Publish(ctx, v1.ConfigID); err != nil {
		t.Fatalf("发布 v1 失败: %v", err)
	}
	v2 := f.forkDraft(t, v1.ConfigID)

	if _, err := service.CutProgress(ctx, "tester", &model.CutProgressRequest{
		ConfigID: v2.ConfigID, CutNum: 0.5,
	}); err != nil {
		t.Fatalf("切流失败: %v", err)
	}

	snap := data.GetSnapshot(ctx, f.proj.Logo, v1.Logo, v1.Version)
	if snap == nil {
		t.Fatal("切流后应存在线上版本快照")
	}
	if snap.CutNum != 0.5 || snap.NewVersion == nil {
		t.Fatalf("切流快照应含灰度：cut_num=%v new_version=%v", snap.CutNum, snap.NewVersion != nil)
	}
	if snap.NewVersion.Version != v2.Version {
		t.Errorf("灰度目标版本错误：want %q got %q", v2.Version, snap.NewVersion.Version)
	}

	// 取消切流 → 老版本自身重投到同一 key，灰度被清掉
	if _, err := service.CutProgress(ctx, "tester", &model.CutProgressRequest{
		ConfigID: v2.ConfigID, CutNum: 0,
	}); err != nil {
		t.Fatalf("取消切流失败: %v", err)
	}
	snap2 := data.GetSnapshot(ctx, f.proj.Logo, v1.Logo, v1.Version)
	if snap2 == nil {
		t.Fatal("取消切流后仍应存在线上版本快照")
	}
	if snap2.CutNum != 0 || snap2.NewVersion != nil {
		t.Errorf("取消切流后快照不应带灰度：cut_num=%v new_version=%v", snap2.CutNum, snap2.NewVersion != nil)
	}
}

// TestEditDraftDuringGrayInvalidatesActiveSnapshot 灰度进行中原地改待上线版本 →
// 生效版本的快照被失效（否则灰度会继续执行旧草稿规则）。
func TestEditDraftDuringGrayInvalidatesActiveSnapshot(t *testing.T) {
	requireRedis(t)
	ctx := context.Background()
	f := newCacheFixture(t)

	v1 := f.createDraft(t, "线上规则", "cfg_cache_gray_")
	if _, err := service.Publish(ctx, v1.ConfigID); err != nil {
		t.Fatalf("发布 v1 失败: %v", err)
	}
	v2 := f.forkDraft(t, v1.ConfigID)
	if _, err := service.CutProgress(ctx, "tester", &model.CutProgressRequest{
		ConfigID: v2.ConfigID, CutNum: 0.3,
	}); err != nil {
		t.Fatalf("切流失败: %v", err)
	}
	if data.GetSnapshot(ctx, f.proj.Logo, v1.Logo, v1.Version) == nil {
		t.Fatal("前置失败：切流后应存在线上版本快照")
	}

	// 灰度期间原地编辑待上线版本
	if _, err := service.UpdateRuleConfig(ctx, "tester", &model.UpdateRuleConfigRequest{
		ConfigID: v2.ConfigID, Name: v2.Name, Rule: f.rule(), ResultType: model.ResultTypePassRejectReview,
		TestData: f.data(1),
	}); err != nil {
		t.Fatalf("灰度期编辑草稿失败: %v", err)
	}

	if data.GetSnapshot(ctx, f.proj.Logo, v1.Logo, v1.Version) != nil {
		t.Error("灰度期编辑待上线版本后应失效线上版本快照（否则灰度继续跑旧草稿规则）")
	}
}

// TestDeleteConfigClearsCachesAndRestoresLatest 删除最新草稿后：
// latest 快照与版本快照被清、is_latest 归还给线上版本。
func TestDeleteConfigClearsCachesAndRestoresLatest(t *testing.T) {
	requireRedis(t)
	ctx := context.Background()
	f := newCacheFixture(t)

	v1 := f.createDraft(t, "线上规则", "cfg_cache_del_")
	if _, err := service.Publish(ctx, v1.ConfigID); err != nil {
		t.Fatalf("发布 v1 失败: %v", err)
	}
	v2 := f.forkDraft(t, v1.ConfigID) // v2 是 is_latest 草稿

	seedLatestSnapshot(t, f.proj.Logo, v1.Logo)
	seedSnapshot(t, f.proj.Logo, v1.Logo, v2.Version)

	if err := service.DeleteConfig(ctx, v2.ConfigID); err != nil {
		t.Fatalf("删除配置失败: %v", err)
	}

	if data.GetLatestSnapshot(ctx, f.proj.Logo, v1.Logo) != nil {
		t.Error("删除后应清掉 latest 草稿快照（否则 offline 继续读到已删草稿）")
	}
	if data.GetSnapshot(ctx, f.proj.Logo, v1.Logo, v2.Version) != nil {
		t.Error("删除后应清掉被删版本的版本化快照")
	}
	latest, err := data.GetLatestConfigByLogo(ctx, v1.Logo)
	if err != nil {
		t.Fatalf("删除 is_latest 版本后应把标记归还给剩余版本，实际查询失败: %v", err)
	}
	if latest.ID != v1.ConfigID {
		t.Errorf("is_latest 应归还给线上版本 v1(id=%d)，实际 id=%d", v1.ConfigID, latest.ID)
	}
}
