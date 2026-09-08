package controller

import (
	"myproject/internal/model"
	"myproject/internal/service"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// Eval 线下测试求值（供程序调用，无登录态）。
// @Router /api/v1/engine/eval [post]
//
// 安全说明：本接口不挂登录态，面向「配置平台被程序调用」的线下测试场景，
// 生产环境应通过部署网络隔离（内网/安全组）或增加 IP 白名单中间件保护。
func Eval(c *gin.Context) {
	var req model.EvalRequest
	if !bindJSON(c, &req) {
		return
	}

	resp, err := service.Eval(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}
