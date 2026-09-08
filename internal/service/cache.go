package service

import (
	"context"
	"strconv"

	"myproject/internal/data"
	"myproject/internal/engine"
	"myproject/internal/model"
)

// packOf 把 config.ProjectID（project 主键字符串）解析成项目标识（logo），
// 作为缓存 key 的 pack 段。解析失败时退回 project_id 兜底（保证 key 稳定不空）。
func packOf(ctx context.Context, projectID string) string {
	id, err := strconv.ParseUint(projectID, 10, 64)
	if err != nil {
		return projectID
	}
	p, err := data.GetProjectByID(ctx, id)
	if err != nil || p.Logo == "" {
		return projectID
	}
	return p.Logo
}

// buildSnapshot 从 DB 组装 config 的全量快照（主表 + 规则 + 指标元信息）。
func buildSnapshot(ctx context.Context, c *model.Config) (*model.ConfigSnapshot, error) {
	snap := &model.ConfigSnapshot{
		Pack:       packOf(ctx, c.ProjectID),
		Extension:  c.Logo,
		Version:    c.Version,
		Type:       c.Type,
		Status:     c.Status,
		CutNum:     c.CutNum,
		CutVersion: c.CutVersion,
	}
	if c.Type == model.ConfigTypeRule {
		// 区分「无规则记录」（正常，快照无 rule）与真实 DB 错误（不能静默吞掉）
		r, err := data.GetRuleByConfigID(ctx, c.ID)
		if err != nil && !data.IsNotFound(err) {
			return nil, err
		}
		if r != nil {
			snap.Rule = &model.RuleSnapshot{Script: r.Rule, ResultType: r.ResultType}
			fields, ferr := data.GetFieldsByIDs(ctx, engine.SplitInt64Slice(r.BindVar))
			if ferr != nil {
				return nil, ferr
			}
			snap.BindVar = toFieldSnapshots(fields)
		}
	}
	return snap, nil
}

// buildSnapshotWithGray 组装快照，并检测灰度标记组装 NewVersion（回源自愈）。
//
// 对应资料 buildExtensionCache 的回源自愈：Redis 冷启动/过期驱逐导致 miss 时，
// 回源 DB 若发现当前生效版本带灰度标记（cut_num∈(0,1) 且 cut_version 非空），
// 则把待上线版本也组装进快照，保证灰度分支在无缓存时仍可正常工作。
func buildSnapshotWithGray(ctx context.Context, c *model.Config) *model.ConfigSnapshot {
	snap, err := buildSnapshot(ctx, c)
	if err != nil {
		return nil
	}
	if c.CutNum > 0 && c.CutNum < 1 && c.CutVersion != "" {
		if nc, err := data.GetConfigByLogoAndVersion(ctx, c.Logo, c.CutVersion); err == nil {
			if nsnap, err := buildSnapshot(ctx, nc); err == nil {
				snap.NewVersion = nsnap
			}
		}
	}
	return snap
}

// toFieldSnapshots 把 model.Field 转成轻量快照。
func toFieldSnapshots(fields []*model.Field) []*model.FieldSnapshot {
	out := make([]*model.FieldSnapshot, 0, len(fields))
	for _, f := range fields {
		out = append(out, &model.FieldSnapshot{
			ID:           f.ID,
			Name:         f.Name,
			Type:         f.Type,
			ParsePath:    f.ParsePath,
			DefaultValue: f.DefaultValue,
		})
	}
	return out
}
