package controller

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/service"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// CreateField 添加字段
// @Summary 添加字段
// @Tags 字段管理
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body model.CreateFieldRequest true "字段信息"
// @Success 200 {object} response.Response{data=model.FieldResponse}
// @Router /api/v1/fields/add [post]
func CreateField(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.CreateFieldRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.CreateField(c.Request.Context(), middleware.Username(c), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateField 更新字段
// @Summary 更新字段
// @Tags 字段管理
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body model.UpdateFieldRequest true "字段信息（含 id）"
// @Success 200 {object} response.Response
// @Router /api/v1/fields/update [post]
func UpdateField(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.UpdateFieldRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := service.UpdateField(c.Request.Context(), req.ID, middleware.Username(c), &req); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}

// DeleteField 删除字段
// @Summary 删除字段
// @Tags 字段管理
// @Produce json
// @Security Bearer
// @Param request body model.DeleteFieldRequest true "字段ID"
// @Success 200 {object} response.Response
// @Router /api/v1/fields/delete [post]
func DeleteField(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.DeleteFieldRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := service.DeleteField(c.Request.Context(), req.ID); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}

// ListFields 获取字段列表
// @Summary 获取字段列表
// @Tags 字段管理
// @Produce json
// @Security Bearer
// @Param page query int false "页码"
// @Param page_size query int false "每页数量，最大 100"
// @Param project_id query string false "所属项目ID"
// @Param name query string false "字段名称（模糊）"
// @Param type query string false "字段类型"
// @Param status query int false "状态 1-生效 2-废弃"
// @Success 200 {object} response.Response{data=response.ListData}
// @Router /api/v1/fields/list [get]
func ListFields(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.FieldListRequest
	if !bindQuery(c, &req) {
		return
	}

	list, total, err := service.ListFields(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}

	page, pageSize := model.PageRequest{Page: req.Page, PageSize: req.PageSize}.Normalize()
	response.SuccessList(c, list, total, page, pageSize)
}

// GetField 获取字段详情
// @Summary 获取字段详情
// @Tags 字段管理
// @Produce json
// @Security Bearer
// @Param id query int true "字段ID"
// @Success 200 {object} response.Response{data=model.FieldResponse}
// @Router /api/v1/fields/detail [get]
func GetField(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.FieldQueryRequest
	if !bindQuery(c, &req) {
		return
	}

	resp, err := service.GetField(c.Request.Context(), req.ID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}
