package service

import (
	"context"
	"time"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/pkg/errcode"
)

// CreateProject 创建项目。
// created_user/updated_user 取当前登录用户名；时间戳为 Unix 秒。
func CreateProject(ctx context.Context, username string, req *model.CreateProjectRequest) (*model.ProjectResponse, error) {
	now := time.Now().Unix()
	status := uint8(req.Status)
	if status != 0 && status != model.ProjectStatusActive && status != model.ProjectStatusRetired {
		return nil, errcode.ErrInvalidParams.WithDetails("status 只能是 1(生效) 或 2(废弃)")
	}
	if status == 0 {
		status = model.ProjectStatusActive
	}

	p := &model.Project{
		Name:        req.Name,
		Logo:        req.Logo,
		Status:      status,
		CreatedUser: username,
		UpdatedUser: username,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := data.CreateProject(ctx, p); err != nil {
		if data.IsDuplicate(err) {
			return nil, errcode.ErrProjectLogoExists.WithDetails("项目标识 %s 已存在", req.Logo)
		}
		return nil, err
	}
	return p.ToResponse(), nil
}

// GetProject 获取项目详情
func GetProject(ctx context.Context, id uint64) (*model.ProjectResponse, error) {
	p, err := getProject(ctx, id)
	if err != nil {
		return nil, err
	}
	return p.ToResponse(), nil
}

// UpdateProject 更新项目（整行编辑：name/logo/status 全量替换）。
func UpdateProject(ctx context.Context, id uint64, username string, req *model.UpdateProjectRequest) error {
	p, err := getProject(ctx, id)
	if err != nil {
		return err
	}

	st := uint8(req.Status)
	if st != model.ProjectStatusActive && st != model.ProjectStatusRetired {
		return errcode.ErrInvalidParams.WithDetails("status 只能是 1(生效) 或 2(废弃)")
	}

	upd := &model.Project{
		Name:        req.Name,
		Logo:        req.Logo,
		Status:      st,
		UpdatedUser: username,
		UpdatedAt:   time.Now().Unix(),
	}

	if _, err := data.UpdateProject(ctx, p.ID, upd); err != nil {
		if data.IsDuplicate(err) {
			return errcode.ErrProjectLogoExists.WithDetails("项目标识 %s 已存在", req.Logo)
		}
		return err
	}
	return nil
}

// DeleteProject 删除项目
func DeleteProject(ctx context.Context, id uint64) error {
	if _, err := getProject(ctx, id); err != nil {
		return err
	}
	return data.DeleteProject(ctx, id)
}

// ListProjects 分页查询项目列表
func ListProjects(ctx context.Context, req *model.ProjectListRequest) ([]*model.ProjectResponse, int64, error) {
	page, pageSize := model.NormalizePage(req.Page, req.PageSize)

	// 筛选时 status=0 视为“不过滤”，避免残留/清空参数触发 400
	var st *uint8
	if req.Status != nil && *req.Status != 0 {
		st = req.Status
	}
	list, total, err := data.ListProjects(ctx, req.Name, st, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	res := make([]*model.ProjectResponse, 0, len(list))
	for _, p := range list {
		res = append(res, p.ToResponse())
	}
	return res, total, nil
}

// getProject 取项目并翻译「不存在」错误
func getProject(ctx context.Context, id uint64) (*model.Project, error) {
	p, err := data.GetProjectByID(ctx, id)
	if err != nil {
		if data.IsNotFound(err) {
			return nil, errcode.ErrProjectNotFound
		}
		return nil, err
	}
	return p, nil
}
