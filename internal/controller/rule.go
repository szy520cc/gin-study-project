package controller

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/service"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// CreateRuleConfig 一步创建规则配置 + 规则内容（规则页「新增规则」）
// @Router /api/v1/configs/rule/add [post]
func CreateRuleConfig(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.CreateRuleConfigRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.CreateRuleConfig(c.Request.Context(), middleware.Username(c), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

// SaveRule 保存规则（含编译 + fork 语义）
// @Router /api/v1/configs/rule/save [post]
func SaveRule(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.SaveRuleRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.SaveRule(c.Request.Context(), middleware.Username(c), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

// GetRule 获取规则详情（含占位符原文供编辑器回显）
// @Router /api/v1/configs/rule/detail [get]
func GetRule(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.ConfigQueryRequest
	if !bindQuery(c, &req) {
		return
	}

	resp, err := service.GetRule(c.Request.Context(), req.ID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

// TestRun 现场验证规则（输入目标参数，不落库不碰缓存）
// @Router /api/v1/configs/testrun [post]
func TestRun(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.TestRunRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.TestRun(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}
