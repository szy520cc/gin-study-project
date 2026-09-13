package service

import (
	"context"
	"strings"
	"time"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/pkg/errcode"
	"myproject/pkg/transaction"
)

// CreateConfig 创建配置。
// logo+version 组合唯一；status 缺省为待审核(0)，is_latest 缺省为是(1)。
func CreateConfig(ctx context.Context, username string, req *model.CreateConfigRequest) (*model.ConfigResponse, error) {
	now := time.Now().Unix()
	isLatest := req.IsLatest
	if isLatest == 0 {
		isLatest = model.ConfigLatestYes
	}

	c := &model.Config{
		ProjectID:    strings.TrimSpace(req.ProjectID),
		ConfigPackID: req.ConfigPackID,
		Name:         strings.TrimSpace(req.Name),
		Logo:         strings.TrimSpace(req.Logo),
		Type:         strings.TrimSpace(req.Type),
		Version:      strings.TrimSpace(req.Version),
		Status:       req.Status, // 0 即待审核，合法状态
		IsLatest:     isLatest,
		Remark:       strings.TrimSpace(req.Remark),
		CreatedUser:  username,
		UpdatedUser:  username,
		CreatedAt:    now,
		UpdatedAt:    now,
		CutNum:       req.CutNum,
		CutAt:        req.CutAt,
		CutVersion:   strings.TrimSpace(req.CutVersion),
	}

	if err := data.CreateConfig(ctx, c); err != nil {
		if data.IsDuplicate(err) {
			return nil, errcode.ErrConfigLogoVersionExists.WithDetails("配置标识 %s 的版本 %s 已存在", c.Logo, c.Version)
		}
		return nil, err
	}
	// 保证同一 logo 下只有这个新创建的版本是最新版本。
	if err := data.EnsureOnlyLatest(ctx, c.Logo, c.ID); err != nil {
		return nil, err
	}
	return c.ToResponse(), nil
}

// GetConfig 获取配置详情
func GetConfig(ctx context.Context, id uint64) (*model.ConfigResponse, error) {
	c, err := getConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	resp := c.ToResponse()
	if err := fillConfigNames(ctx, []*model.ConfigResponse{resp}); err != nil {
		return nil, err
	}
	if err := fillConfigFlags(ctx, []*model.ConfigResponse{resp}); err != nil {
		return nil, err
	}
	if err := fillConfigCutInfo(ctx, []*model.ConfigResponse{resp}); err != nil {
		return nil, err
	}
	return resp, nil
}

// UpdateConfig 更新配置（不含身份字段）。
func UpdateConfig(ctx context.Context, id uint64, username string, req *model.UpdateConfigRequest) error {
	existing, err := getConfig(ctx, id)
	if err != nil {
		return err
	}

	// is_latest 为 0 表示请求没传，不要修改该字段；
	// 显式传 1 时要保证同 logo 下只有当前版本为最新。
	upd := &model.Config{
		Name:        strings.TrimSpace(req.Name),
		Type:        strings.TrimSpace(req.Type),
		Status:      req.Status,
		IsLatest:    req.IsLatest,
		Remark:      strings.TrimSpace(req.Remark),
		UpdatedUser: username,
		UpdatedAt:   time.Now().Unix(),
		CutNum:      req.CutNum,
		CutAt:       req.CutAt,
		CutVersion:  strings.TrimSpace(req.CutVersion),
	}

	if _, err := data.UpdateConfig(ctx, id, upd); err != nil {
		return err
	}
	if req.IsLatest == model.ConfigLatestYes {
		if err := data.EnsureOnlyLatest(ctx, existing.Logo, id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteConfig 删除配置（联动删除其规则子表，避免孤儿数据）
func DeleteConfig(ctx context.Context, id uint64) error {
	if _, err := getConfig(ctx, id); err != nil {
		return err
	}
	return transaction.Do(ctx, func(ctx context.Context) error {
		if err := data.DeleteRuleByConfigID(ctx, id); err != nil {
			return err
		}
		return data.DeleteConfig(ctx, id)
	})
}

// ListConfigs 分页查询配置。
// 列表按「配置」维度展示：同一 logo 只取最新版本，避免满屏都是同一配置的历史版本。
func ListConfigs(ctx context.Context, req *model.ConfigListRequest) ([]*model.ConfigResponse, int64, error) {
	page, pageSize := model.NormalizePage(req.Page, req.PageSize)

	// 列表始终只展示最新版本；历史版本通过「配置详情/版本」查看。
	list, total, err := data.ListConfigs(ctx, req.ProjectID, req.ConfigPackID, req.Name, req.Type, req.Status, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	res := make([]*model.ConfigResponse, 0, len(list))
	for _, c := range list {
		// 列表按 logo 聚合，返回的已经是该 logo 下 id 最大的版本，
		// 因此其 is_latest 语义上一定是 1（避免 DB 中 is_latest 不一致时展示错误）。
		c.IsLatest = model.ConfigLatestYes
		res = append(res, c.ToResponse())
	}
	// 回填项目名称与配置包名称：两个 ID 直接展示都是一串编号
	if err := fillConfigNames(ctx, res); err != nil {
		return nil, 0, err
	}
	// 回填切流/发布前置标记（has_active_version / rule_ready）
	if err := fillConfigFlags(ctx, res); err != nil {
		return nil, 0, err
	}
	// 回填切流信息：从同 logo 的线上版本行取（切流标记写在线上版本上）
	if err := fillConfigCutInfo(ctx, res); err != nil {
		return nil, 0, err
	}
	return res, total, nil
}

func getConfig(ctx context.Context, id uint64) (*model.Config, error) {
	c, err := data.GetConfigByID(ctx, id)
	if err != nil {
		if data.IsNotFound(err) {
			return nil, errcode.ErrConfigNotFound
		}
		return nil, err
	}
	return c, nil
}
