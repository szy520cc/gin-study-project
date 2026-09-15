package controller

import (
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/resource"
	"myproject/internal/service"
	"myproject/pkg/errcode"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// Eval 线下测试求值（供程序调用，需服务令牌）。
// @Router /api/v1/engine/eval [post]
//
// 安全说明：
//   - 本接口不挂用户登录态（程序调用无用户 JWT），改为「服务令牌 + 可选 IP 白名单」，
//     见 middleware.ServiceAuth 与 config.EngineAuthConfig；
//   - offline_flag 会读取「未发布的草稿」（绕过发布链），因此需要同时满足：
//     engine.allow_offline_draft=true 且调用方出示内部令牌（engine.auth.internal_tokens）。
//     草稿预览的常规入口是后台「试运行」（/configs/testrun，需登录）。
func Eval(c *gin.Context) {
	var req model.EvalRequest
	if !bindJSON(c, &req) {
		return
	}

	if req.OfflineFlag {
		if cfg := resource.Cfg(); cfg == nil || !cfg.Engine.AllowOfflineDraft {
			response.Error(c, errcode.ErrEngineOfflineForbidden.WithDetails("offline_flag 未开启"))
			return
		}
		if middleware.ServiceScopeOf(c) != middleware.ScopeInternal {
			response.Error(c, errcode.ErrEngineOfflineForbidden.WithDetails("offline_flag 需要内部令牌"))
			return
		}
	}

	resp, err := service.Eval(c.Request.Context(), &req)
	if err != nil {
		response.Error(c, err)
		return
	}
	response.Success(c, resp)
}
