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
	return c.ToResponse(), nil
}

// GetConfig 获取配置详情
func GetConfig(ctx context.Context, id uint64) (*model.ConfigResponse, error) {
	c, err := getConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	return c.ToResponse(), nil
}

// UpdateConfig 更新配置（不含身份字段）。
func UpdateConfig(ctx context.Context, id uint64, username string, req *model.UpdateConfigRequest) error {
	if _, err := getConfig(ctx, id); err != nil {
		return err
	}

	isLatest := req.IsLatest
	if isLatest == 0 {
		isLatest = model.ConfigLatestYes
	}

	upd := &model.Config{
		Name:        strings.TrimSpace(req.Name),
		Type:        strings.TrimSpace(req.Type),
		Status:      req.Status,
		IsLatest:    isLatest,
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

// ListConfigs 分页查询配置
func ListConfigs(ctx context.Context, req *model.ConfigListRequest) ([]*model.ConfigResponse, int64, error) {
	page, pageSize := model.NormalizePage(req.Page, req.PageSize)

	// 筛选时 is_latest=0 视为“不过滤”，避免残留/清空参数触发 400
	var il *uint8
	if req.IsLatest != nil && *req.IsLatest != 0 {
		il = req.IsLatest
	}
	list, total, err := data.ListConfigs(ctx, req.ConfigPackID, req.Name, req.Type, req.Status, il, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	res := make([]*model.ConfigResponse, 0, len(list))
	for _, c := range list {
		res = append(res, c.ToResponse())
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
