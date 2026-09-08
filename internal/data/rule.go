package data

import (
	"context"

	"myproject/internal/model"

	"gorm.io/gorm"
)

// CreateRule 插入规则记录。
func CreateRule(ctx context.Context, r *model.Rule) error {
	return connDb(ctx).Create(r).Error
}

// GetRuleByConfigID 按 config_id 取规则（业务上 config:rule 是 1:1）。
func GetRuleByConfigID(ctx context.Context, configID uint64) (*model.Rule, error) {
	var r model.Rule
	if err := connDb(ctx).Where("config_id = ?", configID).First(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateRule 更新规则内容（编译后脚本 + 原文 + bind_var + result_type + 冗余字段）。
func UpdateRule(ctx context.Context, ruleID uint64, r *model.Rule) (int64, error) {
	res := connDb(ctx).Model(&model.Rule{}).
		Where("id = ?", ruleID).
		Updates(map[string]interface{}{
			"rule":             r.Rule,
			"condition_config": r.ConditionConfig,
			"bind_var":         r.BindVar,
			"result_type":      r.ResultType,
			"engine":           r.Engine,
			"pack":             r.Pack,
			"extension":        r.Extension,
			"version":          r.Version,
			"updated_at":       r.UpdatedAt,
		})
	return res.RowsAffected, res.Error
}

// CopyRule 复制规则到新 config（fork 新版本时用）。
// 把 oldRule 内容复制一份，绑定到 newConfigID / 新版本号，ID 归零重新插入。
func CopyRule(ctx context.Context, oldRule *model.Rule, newConfigID uint64, newVersion string) (*model.Rule, error) {
	if oldRule == nil {
		return nil, gorm.ErrRecordNotFound
	}
	nr := *oldRule
	nr.ID = 0
	nr.ConfigID = newConfigID
	nr.Version = newVersion
	if err := connDb(ctx).Create(&nr).Error; err != nil {
		return nil, err
	}
	return &nr, nil
}

// DeleteRuleByConfigID 删除 config 关联的规则（config 删除时联动，物理删）。
func DeleteRuleByConfigID(ctx context.Context, configID uint64) error {
	return connDb(ctx).Where("config_id = ?", configID).Delete(&model.Rule{}).Error
}
