package data

import (
	"context"

	"myproject/internal/model"

	"gorm.io/gorm"
)

// CreateConfig 插入配置
func CreateConfig(ctx context.Context, c *model.Config) error {
	return connDb(ctx).Create(c).Error
}

// GetConfigByID 按主键取配置
func GetConfigByID(ctx context.Context, id uint64) (*model.Config, error) {
	var c model.Config
	if err := connDb(ctx).First(&c, id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateConfig 更新可编辑列（身份字段 logo/version/project_id/config_pack_id 不改）
func UpdateConfig(ctx context.Context, id uint64, c *model.Config) (int64, error) {
	res := connDb(ctx).Model(&model.Config{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"name":         c.Name,
			"type":         c.Type,
			"status":       c.Status,
			"is_latest":    c.IsLatest,
			"remark":       c.Remark,
			"updated_user": c.UpdatedUser,
			"updated_at":   c.UpdatedAt,
			"cut_num":      c.CutNum,
			"cut_at":       c.CutAt,
			"cut_version":  c.CutVersion,
		})
	return res.RowsAffected, res.Error
}

// DeleteConfig 物理删除配置
func DeleteConfig(ctx context.Context, id uint64) error {
	return connDb(ctx).Delete(&model.Config{}, id).Error
}

// ListConfigs 分页查询配置。configPackID/name/type/status/isLatest 均为可选条件
func ListConfigs(ctx context.Context, configPackID uint64, name, typ string, status, isLatest *uint8, page, pageSize int) ([]*model.Config, int64, error) {
	query := func() *gorm.DB {
		q := connDb(ctx).Model(&model.Config{})
		if configPackID != 0 {
			q = q.Where("config_pack_id = ?", configPackID)
		}
		if name != "" {
			q = q.Where("name LIKE ?", "%"+name+"%")
		}
		if typ != "" {
			q = q.Where("type = ?", typ)
		}
		if status != nil {
			q = q.Where("status = ?", *status)
		}
		if isLatest != nil {
			q = q.Where("is_latest = ?", *isLatest)
		}
		return q
	}

	var total int64
	if err := query().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	var list []*model.Config
	err := query().Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
