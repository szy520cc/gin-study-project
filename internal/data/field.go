package data

import (
	"context"

	"myproject/internal/model"

	"gorm.io/gorm"
)

// CreateField 插入一条字段记录
func CreateField(ctx context.Context, f *model.Field) error {
	return connDb(ctx).Create(f).Error
}

// GetFieldByID 按主键取字段
func GetFieldByID(ctx context.Context, id uint64) (*model.Field, error) {
	var f model.Field
	if err := connDb(ctx).First(&f, id).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

// UpdateField 整行更新可编辑列
func UpdateField(ctx context.Context, id uint64, f *model.Field) (int64, error) {
	res := connDb(ctx).Model(&model.Field{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"project_id":    f.ProjectID,
			"name":          f.Name,
			"type":          f.Type,
			"default_value": f.DefaultValue,
			"parse_path":    f.ParsePath,
			"status":        f.Status,
			"remark":        f.Remark,
			"updated_user":  f.UpdatedUser,
			"updated_at":    f.UpdatedAt,
		})
	return res.RowsAffected, res.Error
}

// DeleteField 物理删除字段
func DeleteField(ctx context.Context, id uint64) error {
	return connDb(ctx).Delete(&model.Field{}, id).Error
}

// ListFields 分页查询字段。
// projectID / name（模糊）/ type / status 均为可选条件；type 与 status 为空串/nil 表示不过滤。
func ListFields(ctx context.Context, projectID, name, typ string, status *uint8, page, pageSize int) ([]*model.Field, int64, error) {
	query := func() *gorm.DB {
		q := connDb(ctx).Model(&model.Field{})
		if projectID != "" {
			q = q.Where("project_id = ?", projectID)
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
		return q
	}

	var total int64
	if err := query().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	var list []*model.Field
	err := query().Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
