package data

import (
	"context"

	"myproject/internal/model"

	"gorm.io/gorm"
)

// CreateConfigPack 插入配置包
func CreateConfigPack(ctx context.Context, c *model.ConfigPack) error {
	return connDb(ctx).Create(c).Error
}

// GetConfigPackByID 按主键取配置包
func GetConfigPackByID(ctx context.Context, id uint64) (*model.ConfigPack, error) {
	var c model.ConfigPack
	if err := connDb(ctx).First(&c, id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateConfigPack 更新可编辑列（name/status/remark，不含 logo）
func UpdateConfigPack(ctx context.Context, id uint64, c *model.ConfigPack) (int64, error) {
	res := connDb(ctx).Model(&model.ConfigPack{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"name":         c.Name,
			"status":       c.Status,
			"remark":       c.Remark,
			"updated_user": c.UpdatedUser,
			"updated_at":   c.UpdatedAt,
		})
	return res.RowsAffected, res.Error
}

// DeleteConfigPack 物理删除配置包
func DeleteConfigPack(ctx context.Context, id uint64) error {
	return connDb(ctx).Delete(&model.ConfigPack{}, id).Error
}

// ListConfigPacks 分页查询配置包。projectID/name/status 均为可选条件
func ListConfigPacks(ctx context.Context, projectID, name string, status *uint8, page, pageSize int) ([]*model.ConfigPack, int64, error) {
	query := func() *gorm.DB {
		q := connDb(ctx).Model(&model.ConfigPack{})
		if projectID != "" {
			q = q.Where("project_id = ?", projectID)
		}
		if name != "" {
			q = q.Where("name LIKE ?", "%"+name+"%")
		}
		if status != nil {
			q = q.Where("status = ?", *status)
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

	var list []*model.ConfigPack
	err := query().Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
