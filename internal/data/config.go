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

// MarkConfigNotLatest 把 is_latest 置为否（编辑生效版本 fork 新版本时，把老版本标记掉）。
func MarkConfigNotLatest(ctx context.Context, id uint64) error {
	return connDb(ctx).Model(&model.Config{}).
		Where("id = ?", id).
		Update("is_latest", model.ConfigLatestNo).Error
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

// CountConfigsByLogo 统计同 logo 的记录数（新建扩展时「标识全局查重」用）。
func CountConfigsByLogo(ctx context.Context, logo string) (int64, error) {
	var total int64
	err := connDb(ctx).Model(&model.Config{}).Where("logo = ?", logo).Count(&total).Error
	return total, err
}

// GetActiveConfigByLogo 查同 logo 的生效版本（status=1）。
// 灰度切流时用于定位「当前线上版本」；返回 gorm.ErrRecordNotFound 表示无线上版本。
func GetActiveConfigByLogo(ctx context.Context, logo string) (*model.Config, error) {
	var c model.Config
	if err := connDb(ctx).Where("logo = ? AND status = ?", logo, model.ConfigStatusActive).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// GetConfigByLogoAndVersion 按 logo+version 查配置（eval 回源锁定指定版本用）。
func GetConfigByLogoAndVersion(ctx context.Context, logo, version string) (*model.Config, error) {
	var c model.Config
	if err := connDb(ctx).Where("logo = ? AND version = ?", logo, version).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// GetLatestConfigByLogo 按 logo 取最新草稿（is_latest=1）。
// offline 场景回源「草稿优先」用；返回 gorm.ErrRecordNotFound 表示无记录。
func GetLatestConfigByLogo(ctx context.Context, logo string) (*model.Config, error) {
	var c model.Config
	if err := connDb(ctx).Where("logo = ? AND is_latest = ?", logo, model.ConfigLatestYes).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ListConfigsByLogo 查同 logo 的全部版本（按 status 升序、version 降序）。
func ListConfigsByLogo(ctx context.Context, logo string) ([]*model.Config, error) {
	var list []*model.Config
	err := connDb(ctx).Where("logo = ?", logo).
		Order("status ASC, version DESC").Find(&list).Error
	return list, err
}

// OfflineOtherVersions 下线同 logo 的其它生效版本并清空灰度字段（publish 用）。
// excludeID 是要发布（待激活）的版本，不在此列；其余 status=1 的全部下线（1→2）。
func OfflineOtherVersions(ctx context.Context, logo string, excludeID uint64) error {
	return connDb(ctx).Model(&model.Config{}).
		Where("logo = ? AND status = ? AND id <> ?", logo, model.ConfigStatusActive, excludeID).
		Updates(map[string]interface{}{
			"status":      model.ConfigStatusOffline,
			"cut_num":     0,
			"cut_version": "",
			"cut_by":      "",
			"cut_at":      0,
		}).Error
}

// ActiveConfig 激活配置（status 0→1）。
func ActiveConfig(ctx context.Context, id uint64) error {
	return connDb(ctx).Model(&model.Config{}).
		Where("id = ? AND status = ?", id, model.ConfigStatusPending).
		Update("status", model.ConfigStatusActive).Error
}

// UpdateConfigCut 写切流字段到「当前生效版本」行（cutprogress 用）。
func UpdateConfigCut(ctx context.Context, id uint64, cutNum float64, cutVersion, operator string, now int64) error {
	return connDb(ctx).Model(&model.Config{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"cut_num":     cutNum,
			"cut_version": cutVersion,
			"cut_by":      operator,
			"cut_at":      now,
		}).Error
}
