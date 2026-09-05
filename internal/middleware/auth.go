package middleware

import (
	"errors"
	"strings"

	"myproject/internal/resource"
	"myproject/pkg/errcode"
	"myproject/pkg/logger"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// CtxUserID 当前登录用户 ID 在 gin.Context 中的 key。
const CtxUserID = "user_id"

// CtxUsername 当前登录用户名在 gin.Context 中的 key。
//
// 业务里「谁创建的/谁最后编辑的」这类审计列（如 project.created_user）需要用户名，
// 只放 ID 还得多查一次用户表；用户名同时已补进 ctx logger 供日志使用（见下）。
const CtxUsername = "username"

// Auth JWT 认证中间件。
// 校验器从 resource 取，不再由调用方注入 —— 进程内只有一份 JWTManager。
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		jwtManager := resource.JWT()
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Error(c, errcode.ErrTokenNotFound)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Error(c, errcode.ErrTokenInvalid.WithDetails("Authorization 头格式应为 Bearer <token>"))
			return
		}

		claims, err := jwtManager.ParseToken(parts[1])
		if err != nil {
			// 区分过期与非法，前端才能决定是刷新还是重新登录
			if errors.Is(err, jwt.ErrTokenExpired) {
				response.Error(c, errcode.ErrTokenExpired)
				return
			}
			response.Error(c, errcode.ErrTokenInvalid.WithCause(err))
			return
		}

		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxUsername, claims.Username)

		// 认证成功后把用户信息补进 ctx logger，后续所有日志自动带 user_id / username
		l := logger.C(c.Request.Context()).With("user_id", claims.UserID, "username", claims.Username)
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context(), l))

		c.Next()
	}
}

// RequireUserID 取当前登录用户 ID，取不到就直接回 401 并中断请求，返回 false。
//
// controller 一律用这个，而不是自己从 ctx 取值后「取不到就返回 0」：
// fail-open 的写法下，一旦某个新路由组漏挂 Auth，接口不会 401，
// 而是拿 user_id=0 去读写数据 —— 落一批归属为 0 的订单，
// 或把 user_id=0 的数据返回给匿名调用方。
func RequireUserID(c *gin.Context) (uint64, bool) {
	if v, ok := c.Get(CtxUserID); ok {
		if id, ok := v.(uint64); ok && id != 0 {
			return id, true
		}
	}
	// 走到这里说明路由没挂 Auth（或 Auth 之后身份被清掉了），属于装配错误，
	// 对调用方只回 401，真实原因进日志。
	logger.C(c.Request.Context()).Error("路由缺少认证中间件或身份缺失",
		"path", c.FullPath(), "method", c.Request.Method)
	response.Error(c, errcode.ErrUnauthorized)
	return 0, false
}

// Username 取当前登录用户名。仅在 Auth 之后的 controller 里使用；
// 取不到时返回空串（业务层应避免把空用户名落进审计列）。
func Username(c *gin.Context) string {
	if v, ok := c.Get(CtxUsername); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
