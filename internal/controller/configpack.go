package controller

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/service"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// CreateConfigPack 添加配置包
// @Router /api/v1/config-packs/add [post]
func CreateConfigPack(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.CreateConfigPackRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.CreateConfigPack(c.Request.Context(), middleware.Username(c), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

// UpdateConfigPack 更新配置包（logo 不可改）
// @Router /api/v1/config-packs/update [post]
func UpdateConfigPack(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.UpdateConfigPackRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := service.UpdateConfigPack(c.Request.Context(), req.ID, middleware.Username(c), &req); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}

// DeleteConfigPack 删除配置包
// @Router /api/v1/config-packs/delete [post]
func DeleteConfigPack(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.DeleteConfigPackRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := service.DeleteConfigPack(c.Request.Context(), req.ID); err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, nil)
}

// ListConfigPacks 获取配置包列表
// @Router /api/v1/config-packs/list [get]
func ListConfigPacks(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.ConfigPackListRequest
	if !bindQuery(c, &req) {
		return
	}

	list, total, err := service.ListConfigPacks(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}

	page, pageSize := model.PageRequest{Page: req.Page, PageSize: req.PageSize}.Normalize()
	response.SuccessList(c, list, total, page, pageSize)
}

// GetConfigPack 获取配置包详情
// @Router /api/v1/config-packs/detail [get]
func GetConfigPack(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.ConfigPackQueryRequest
	if !bindQuery(c, &req) {
		return
	}

	resp, err := service.GetConfigPack(c.Request.Context(), req.ID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}
