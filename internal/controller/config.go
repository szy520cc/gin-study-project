package controller

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/service"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// CreateConfig 添加配置
// @Router /api/v1/configs/add [post]
func CreateConfig(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.CreateConfigRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.CreateConfig(c.Request.Context(), middleware.Username(c), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateConfig 更新配置（身份字段不可改）
// @Router /api/v1/configs/update [post]
func UpdateConfig(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.UpdateConfigRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := service.UpdateConfig(c.Request.Context(), req.ID, middleware.Username(c), &req); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}

// DeleteConfig 删除配置
// @Router /api/v1/configs/delete [post]
func DeleteConfig(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.DeleteConfigRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := service.DeleteConfig(c.Request.Context(), req.ID); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}

// ListConfigs 获取配置列表
// @Router /api/v1/configs/list [get]
func ListConfigs(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.ConfigListRequest
	if !bindQuery(c, &req) {
		return
	}

	list, total, err := service.ListConfigs(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}

	page, pageSize := model.PageRequest{Page: req.Page, PageSize: req.PageSize}.Normalize()
	response.SuccessList(c, list, total, page, pageSize)
}

// GetConfig 获取配置详情
// @Router /api/v1/configs/detail [get]
func GetConfig(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.ConfigQueryRequest
	if !bindQuery(c, &req) {
		return
	}

	resp, err := service.GetConfig(c.Request.Context(), req.ID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}
