package controller

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/service"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// Publish 全量发布
// @Router /api/v1/configs/publish [post]
func Publish(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.PublishRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.Publish(c.Request.Context(), req.ID)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}

// CutProgress 灰度切流
// @Router /api/v1/configs/cutprogress [post]
func CutProgress(c *gin.Context) {
	if _, ok := middleware.RequireUserID(c); !ok {
		return
	}

	var req model.CutProgressRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.CutProgress(c.Request.Context(), middleware.Username(c), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}
