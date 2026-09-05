package data

import (
	"context"

	"myproject/internal/model"

	"gorm.io/gorm"
)

// CreateProject 插入一条项目记录
func CreateProject(ctx context.Context, p *model.Project) error {
	return connDb(ctx).Create(p).Error
}

// GetProjectByID 按主键取项目，不存在时返回 gorm.ErrRecordNotFound。
// 业务层负责把该错误翻译成 errcode。
func GetProjectByID(ctx context.Context, id uint64) (*model.Project, error) {
	var p model.Project
	if err := connDb(ctx).First(&p, id).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateProject 整行更新可编辑字段（Updates 只更新非零列）。
// 调用方必须先 Get 确认记录存在；返回 RowsAffected 供并发场景参考。
func UpdateProject(ctx context.Context, id uint64, p *model.Project) (int64, error) {
	res := connDb(ctx).Model(&model.Project{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"name":         p.Name,
			"logo":         p.Logo,
			"status":       p.Status,
			"updated_user": p.UpdatedUser,
			"updated_at":   p.UpdatedAt,
		})
	return res.RowsAffected, res.Error
}

// DeleteProject 物理删除项目
func DeleteProject(ctx context.Context, id uint64) error {
	return connDb(ctx).Delete(&model.Project{}, id).Error
}

// ListProjects 分页查询项目。
// name 传非空做模糊匹配；status 为 nil 表示不限状态。
// page/pageSize 由 service 归一化后传入。
func ListProjects(ctx context.Context, name string, status *uint8, page, pageSize int) ([]*model.Project, int64, error) {
	query := func() *gorm.DB {
		q := connDb(ctx).Model(&model.Project{})
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

	var list []*model.Project
	err := query().Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
