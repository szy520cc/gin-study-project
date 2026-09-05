package controller

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/service"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// CreateProject 创建项目
// @Summary 创建项目
// @Tags 项目管理
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body model.CreateProjectRequest true "项目信息"
// @Success 200 {object} response.Response{data=model.ProjectResponse}
// @Router /api/v1/projects/add [post]
func CreateProject(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.CreateProjectRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.CreateProject(c.Request.Context(), middleware.Username(c), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

// GetProject 获取项目详情
// @Summary 获取项目详情
// @Tags 项目管理
// @Produce json
// @Security Bearer
// @Param id query int true "项目ID"
// @Success 200 {object} response.Response{data=model.ProjectResponse}
// @Router /api/v1/projects/detail [get]
func GetProject(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.ProjectQueryRequest
	if !bindQuery(c, &req) {
		return
	}

	resp, err := service.GetProject(c.Request.Context(), req.ID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateProject 更新项目
// @Summary 更新项目
// @Tags 项目管理
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body model.UpdateProjectRequest true "项目信息（含 id）"
// @Success 200 {object} response.Response
// @Router /api/v1/projects/update [post]
func UpdateProject(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.UpdateProjectRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := service.UpdateProject(c.Request.Context(), req.ID, middleware.Username(c), &req); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}

// DeleteProject 删除项目
// @Summary 删除项目
// @Tags 项目管理
// @Produce json
// @Security Bearer
// @Param request body model.DeleteProjectRequest true "项目ID"
// @Success 200 {object} response.Response
// @Router /api/v1/projects/delete [post]
func DeleteProject(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.DeleteProjectRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := service.DeleteProject(c.Request.Context(), req.ID); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}

// ListProjects 获取项目列表
// @Summary 获取项目列表
// @Tags 项目管理
// @Produce json
// @Security Bearer
// @Param page query int false "页码"
// @Param page_size query int false "每页数量，最大 100"
// @Param name query string false "项目名称（模糊）"
// @Param status query int false "状态 1-生效 2-废弃"
// @Success 200 {object} response.Response{data=response.ListData}
// @Router /api/v1/projects/list [get]
func ListProjects(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.ProjectListRequest
	if !bindQuery(c, &req) {
		return
	}

	list, total, err := service.ListProjects(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}

	page, pageSize := model.PageRequest{Page: req.Page, PageSize: req.PageSize}.Normalize()
	response.SuccessList(c, list, total, page, pageSize)
}
