package data

import (
	"context"

	"myproject/internal/model"

	"gorm.io/gorm/clause"
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

// UpdateConfigBasic 仅更新基本可编辑字段（name/remark），不碰 status/is_latest/type/cut。
// 用于「整配置一次保存」：fork 新版本后把本次提交的名称/备注写到新版本行，
// 或草稿原地更新名称/备注 —— 都不该动状态与切流字段。
func UpdateConfigBasic(ctx context.Context, id uint64, name, remark, username string, now int64) error {
	return connDb(ctx).Model(&model.Config{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"name":         name,
			"remark":       remark,
			"updated_user": username,
			"updated_at":   now,
		}).Error
}

// DeleteConfig 物理删除配置
func DeleteConfig(ctx context.Context, id uint64) error {
	return connDb(ctx).Delete(&model.Config{}, id).Error
}

// EnsureOnlyLatest 把同 logo 下除了 id 以外的所有版本都置为 is_latest=2，
// 保证每个配置（logo）只有一个最新版本。
func EnsureOnlyLatest(ctx context.Context, logo string, id uint64) error {
	return connDb(ctx).Model(&model.Config{}).
		Where("logo = ? AND id <> ?", logo, id).
		Update("is_latest", model.ConfigLatestNo).Error
}

// ListConfigs 分页查询配置（按 logo 维度聚合，只取每个配置的最新版本）。
// projectID/configPackID/name/type/status 均为可选条件。
func ListConfigs(ctx context.Context, projectID string, configPackID uint64, name, typ string, status *uint8, page, pageSize int) ([]*model.Config, int64, error) {
	db := connDb(ctx)

	// 先按过滤条件拿到每个 logo 的最新记录 id（以自增 id 倒序为最新版本）。
	sub := db.Model(&model.Config{}).Select("MAX(id) AS id").Group("logo")
	if projectID != "" {
		sub = sub.Where("project_id = ?", projectID)
	}
	if configPackID != 0 {
		sub = sub.Where("config_pack_id = ?", configPackID)
	}
	if name != "" {
		sub = sub.Where("name LIKE ?", "%"+name+"%")
	}
	if typ != "" {
		sub = sub.Where("type = ?", typ)
	}
	if status != nil {
		sub = sub.Where("status = ?", *status)
	}

	query := db.Model(&model.Config{}).Where("id IN (?)", sub)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	var list []*model.Config
	err := query.Order("id DESC").
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

// ActiveLogosAmong 批量查询「存在线上版本（status=1）」的 logo 集合。
//
// 列表页据此回填 has_active_version：切流的硬前提是新旧版本共存——
// 灰度状态要挂在线上版本行上，没有线上版本就无处可挂。
func ActiveLogosAmong(ctx context.Context, logos []string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(logos))
	if len(logos) == 0 {
		return out, nil
	}
	var found []string
	if err := connDb(ctx).Model(&model.Config{}).
		Where("logo IN ? AND status = ?", logos, model.ConfigStatusActive).
		Distinct().Pluck("logo", &found).Error; err != nil {
		return nil, err
	}
	for _, l := range found {
		out[l] = struct{}{}
	}
	return out, nil
}

// GetActiveConfigsAmong 批量取各 logo 的「线上版本（status=1）」整行，
// 用于列表回填切流信息：切流时灰度标记写在线上版本行上，
// 而列表按 logo 聚合展示的是最新版本（可能是待审核），切流信息需要从线上版本取。
func GetActiveConfigsAmong(ctx context.Context, logos []string) (map[string]*model.Config, error) {
	out := make(map[string]*model.Config, len(logos))
	if len(logos) == 0 {
		return out, nil
	}
	var list []*model.Config
	if err := connDb(ctx).Model(&model.Config{}).
		Where("logo IN ? AND status = ?", logos, model.ConfigStatusActive).
		Find(&list).Error; err != nil {
		return nil, err
	}
	for _, c := range list {
		out[c.Logo] = c
	}
	return out, nil
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

// ClearConfigCut 清空切流字段（取消切流用）：回到「从未切流」的干净状态，
// 不留操作者/时间残留（口径与 OfflineOtherVersions 的清空一致）。
func ClearConfigCut(ctx context.Context, id uint64) error {
	return connDb(ctx).Model(&model.Config{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"cut_num":     0,
			"cut_version": "",
			"cut_by":      "",
			"cut_at":      0,
		}).Error
}

// LockConfigsByLogo 锁定同 logo 的全部版本行（SELECT ... FOR UPDATE）。
//
// 必须在本包事务内调用（见 pkg/transaction）：用于把「发布 / 切流」这类
// 「读状态 → 写状态」的多步操作串行化，避免并发交错。发布与切流共用本函数，
// 因此二者互斥（同一 logo 同一时刻只有一个在推进）。
func LockConfigsByLogo(ctx context.Context, logo string) ([]*model.Config, error) {
	var list []*model.Config
	err := connDb(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("logo = ?", logo).Order("id").Find(&list).Error
	return list, err
}

// RestoreLatest 把「最新版本（is_latest=1）」标记归还给同 logo 下剩余的版本：
// 优先生效版本（status=1），没有生效版本则取 id 最大的版本；已无任何版本时不动。
//
// 用于删除 is_latest 版本后的修正：否则 is_latest=1 在库里查不到任何行，
// 「最新草稿」这条语义失效（列表按 id 聚合仍可用，但语义不自洽）。
func RestoreLatest(ctx context.Context, logo string) error {
	var c model.Config
	err := connDb(ctx).Where("logo = ? AND status = ?", logo, model.ConfigStatusActive).
		Order("id DESC").First(&c).Error
	if err != nil {
		if !IsNotFound(err) {
			return err
		}
		if err := connDb(ctx).Where("logo = ?", logo).Order("id DESC").First(&c).Error; err != nil {
			if IsNotFound(err) {
				return nil // 该 logo 已无任何版本
			}
			return err
		}
	}
	return connDb(ctx).Model(&model.Config{}).Where("id = ?", c.ID).
		Update("is_latest", model.ConfigLatestYes).Error
}
