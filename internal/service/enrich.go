package service

import (
	"context"
	"strconv"
	"strings"

	"myproject/internal/data"
	"myproject/internal/model"
)

// 本文件集中处理「响应里的可读名称回填」。
//
// 背景：field / config_pack / config 三张表都用 project_id（字符串主键）关联项目，
// config 还额外用 config_pack_id（数字主键）关联配置包。这些 ID 直接展示给运营
// 就是一串编号，所以统一由 service 层批量查名称，回填到响应的 project_name /
// config_pack_name 字段。
//
// 为什么放在 service 而不是 model.ToResponse()：ToResponse 是纯函数（不接收 ctx、
// 不查库），一旦让它查库，任何一次序列化都会隐式产生 SQL；名称回填属于「组装响应」
// 的业务动作，由调用方显式决定要不要查，边界才清楚。

// projectNameMap 批量查项目名称，返回 project.id → name。
//
// 入参是各表里存的项目 ID（字符串形式，如 "48"）。非数字、0 等脏值直接忽略：
// 项目被删除后历史数据里的 project_id 会指向不存在的行，此时名称回填为空即可，
// 不该让整个列表报错。
func projectNameMap(ctx context.Context, ids []string) (map[uint64]string, error) {
	uniq := make([]uint64, 0, len(ids))
	seen := make(map[uint64]struct{}, len(ids))
	for _, s := range ids {
		id, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
		if err != nil || id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return map[uint64]string{}, nil
	}

	list, err := data.GetProjectsByIDs(ctx, uniq)
	if err != nil {
		return nil, err
	}
	m := make(map[uint64]string, len(list))
	for _, p := range list {
		m[p.ID] = p.Name
	}
	return m, nil
}

// lookupProjectName 从名称表里取项目名称，ID 非法或项目已删时返回空串。
func lookupProjectName(m map[uint64]string, projectID string) string {
	id, err := strconv.ParseUint(strings.TrimSpace(projectID), 10, 64)
	if err != nil {
		return ""
	}
	return m[id]
}

// configPackNameMap 批量查配置包名称，返回 config_pack.id → name。
func configPackNameMap(ctx context.Context, ids []uint64) (map[uint64]string, error) {
	uniq := make([]uint64, 0, len(ids))
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return map[uint64]string{}, nil
	}

	list, err := data.GetConfigPacksByIDs(ctx, uniq)
	if err != nil {
		return nil, err
	}
	m := make(map[uint64]string, len(list))
	for _, c := range list {
		m[c.ID] = c.Name
	}
	return m, nil
}

// fillFieldProjectNames 给字段响应回填项目名称（列表/详情共用）。
func fillFieldProjectNames(ctx context.Context, res []*model.FieldResponse) error {
	if len(res) == 0 {
		return nil
	}
	ids := make([]string, 0, len(res))
	for _, r := range res {
		ids = append(ids, r.ProjectID)
	}
	m, err := projectNameMap(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range res {
		r.ProjectName = lookupProjectName(m, r.ProjectID)
	}
	return nil
}

// fillConfigPackProjectNames 给配置包响应回填项目名称（列表/详情共用）。
func fillConfigPackProjectNames(ctx context.Context, res []*model.ConfigPackResponse) error {
	if len(res) == 0 {
		return nil
	}
	ids := make([]string, 0, len(res))
	for _, r := range res {
		ids = append(ids, r.ProjectID)
	}
	m, err := projectNameMap(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range res {
		r.ProjectName = lookupProjectName(m, r.ProjectID)
	}
	return nil
}

// fillConfigNames 给配置响应回填项目名称 + 配置包名称（列表/详情共用）。
//
// 两次批量查询而不是逐行查：一页 100 条时是 2 次 SQL 而不是 200 次。
func fillConfigNames(ctx context.Context, res []*model.ConfigResponse) error {
	if len(res) == 0 {
		return nil
	}
	ids := make([]string, 0, len(res))
	packIDs := make([]uint64, 0, len(res))
	for _, r := range res {
		ids = append(ids, r.ProjectID)
		packIDs = append(packIDs, r.ConfigPackID)
	}

	pm, err := projectNameMap(ctx, ids)
	if err != nil {
		return err
	}
	cm, err := configPackNameMap(ctx, packIDs)
	if err != nil {
		return err
	}

	for _, r := range res {
		r.ProjectName = lookupProjectName(pm, r.ProjectID)
		r.ConfigPackName = cm[r.ConfigPackID]
	}
	return nil
}

// fillConfigFlags 给配置响应回填「切流 / 发布」的前置条件标记（列表/详情共用）。
//
// 两次批量查询：
//   - has_active_version：同 logo 是否存在 status=1 的线上版本
//     （切流要把灰度状态挂在线上版本行上，没有线上版本就无处可挂）；
//   - rule_ready：该 config 是否已保存规则内容
//     （保存时已通过校验，因此「存在」即「可用」；无规则则上线后 eval 报「规则未配置」）。
//
// 前端据此决定「切流 / 发布」按钮是否可见，避免用户点了才被后端拒绝。
func fillConfigFlags(ctx context.Context, res []*model.ConfigResponse) error {
	if len(res) == 0 {
		return nil
	}
	logos := make([]string, 0, len(res))
	ids := make([]uint64, 0, len(res))
	for _, r := range res {
		logos = append(logos, r.Logo)
		ids = append(ids, r.ID)
	}

	activeLogos, err := data.ActiveLogosAmong(ctx, logos)
	if err != nil {
		return err
	}
	ruleIDs, err := data.RuleConfigIDSet(ctx, ids)
	if err != nil {
		return err
	}

	for _, r := range res {
		_, r.HasActiveVersion = activeLogos[r.Logo]
		_, r.RuleReady = ruleIDs[r.ID]
	}
	return nil
}
