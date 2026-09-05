package service

import (
	"context"
	"strings"
	"time"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/pkg/errcode"
)

// CreateConfigPack 创建配置包。
// logo 创建后不可改；logo+project 组合唯一，冲突报业务错误。
// status 未传时默认 0（待审核）。
func CreateConfigPack(ctx context.Context, username string, req *model.CreateConfigPackRequest) (*model.ConfigPackResponse, error) {
	now := time.Now().Unix()

	c := &model.ConfigPack{
		ProjectID:   strings.TrimSpace(req.ProjectID),
		Name:        strings.TrimSpace(req.Name),
		Logo:        strings.TrimSpace(req.Logo),
		Status:      req.Status, // 0 即待审核，是合法业务状态
		Remark:      strings.TrimSpace(req.Remark),
		CreatedUser: username,
		UpdatedUser: username,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := data.CreateConfigPack(ctx, c); err != nil {
		if data.IsDuplicate(err) {
			return nil, errcode.ErrConfigPackLogoExists.WithDetails("该项目下配置包标识 %s 已存在", c.Logo)
		}
		return nil, err
	}
	return c.ToResponse(), nil
}

// GetConfigPack 获取配置包详情
func GetConfigPack(ctx context.Context, id uint64) (*model.ConfigPackResponse, error) {
	c, err := getConfigPack(ctx, id)
	if err != nil {
		return nil, err
	}
	return c.ToResponse(), nil
}

// UpdateConfigPack 更新配置包（仅 name/status/remark；logo 不可修改）。
func UpdateConfigPack(ctx context.Context, id uint64, username string, req *model.UpdateConfigPackRequest) error {
	if _, err := getConfigPack(ctx, id); err != nil {
		return err
	}

	upd := &model.ConfigPack{
		Name:        strings.TrimSpace(req.Name),
		Status:      req.Status,
		Remark:      strings.TrimSpace(req.Remark),
		UpdatedUser: username,
		UpdatedAt:   time.Now().Unix(),
	}

	if _, err := data.UpdateConfigPack(ctx, id, upd); err != nil {
		return err
	}
	return nil
}

// DeleteConfigPack 删除配置包
func DeleteConfigPack(ctx context.Context, id uint64) error {
	if _, err := getConfigPack(ctx, id); err != nil {
		return err
	}
	return data.DeleteConfigPack(ctx, id)
}

// ListConfigPacks 分页查询配置包
func ListConfigPacks(ctx context.Context, req *model.ConfigPackListRequest) ([]*model.ConfigPackResponse, int64, error) {
	page, pageSize := model.NormalizePage(req.Page, req.PageSize)

	list, total, err := data.ListConfigPacks(ctx, req.ProjectID, req.Name, req.Status, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	res := make([]*model.ConfigPackResponse, 0, len(list))
	for _, c := range list {
		res = append(res, c.ToResponse())
	}
	return res, total, nil
}

func getConfigPack(ctx context.Context, id uint64) (*model.ConfigPack, error) {
	c, err := data.GetConfigPackByID(ctx, id)
	if err != nil {
		if data.IsNotFound(err) {
			return nil, errcode.ErrConfigPackNotFound
		}
		return nil, err
	}
	return c, nil
}
