package controller

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/service"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// DeleteConfig 删除配置
// @Router /api/v1/configs/delete [post]
func DeleteConfig(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.IDRequest
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
// @Summary 获取配置列表
// @Tags 配置管理
// @Produce json
// @Security Bearer
// @Param page query int false "页码"
// @Param page_size query int false "每页数量，最大 100"
// @Param project_id query string false "所属项目ID"
// @Param config_pack_id query int false "所属配置包ID"
// @Param name query string false "配置名称（模糊）"
// @Param type query string false "配置类型"
// @Param status query int false "状态 0-待审核 1-生效 2-下线"
// @Success 200 {object} response.Response{data=response.ListData}
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

// ImportConfigFields 根据规则占位符导入字段默认值 JSON
// @Summary 根据规则导入字段默认值 JSON
// @Tags 配置管理
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body model.ImportConfigFieldsRequest true "项目ID + 规则原文"
// @Success 200 {object} response.Response{data=model.ImportConfigFieldsResponse}
// @Router /api/v1/configs/import-fields [post]
func ImportConfigFields(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.ImportConfigFieldsRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.ImportConfigFields(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}
