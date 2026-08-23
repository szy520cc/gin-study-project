package middleware

import (
	"errors"
	"strconv"
	"strings"

	"myproject/pkg/auth"
	"myproject/pkg/errcode"
	"myproject/pkg/logger"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// CtxUserID 当前登录用户 ID 在 gin.Context 中的 key。
//
// 不再往 Context 里放 username：它此前只写不读，属于死代码。
// 用户名对排查/审计有用的地方是日志，所以改为直接补进 ctx logger（见下）。
const CtxUserID = "user_id"

// Auth JWT 认证中间件
func Auth(jwtManager *auth.JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		// 认证成功后把用户信息补进 ctx logger，后续所有日志自动带 user_id / username
		l := logger.C(c.Request.Context()).With("user_id", claims.UserID, "username", claims.Username)
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context(), l))

		c.Next()
	}
}

// RequireUserID 取当前登录用户 ID，取不到就直接回 401 并中断请求，返回 false。
//
// handler 一律用这个而不是 CurrentUserID：后者取不到身份时返回 0（fail-open），
// 一旦某个新路由组漏挂 Auth，接口不会 401，而是拿 user_id=0 去读写数据 ——
// 落一批归属为 0 的订单，或把 user_id=0 的数据返回给匿名调用方。
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

// CurrentUserID 获取当前登录用户 ID，取不到返回 0。
// 只适合日志、审计这类「没有也能继续」的场景；鉴权路径用 RequireUserID。
func CurrentUserID(c *gin.Context) uint64 {
	if v, ok := c.Get(CtxUserID); ok {
		if id, ok := v.(uint64); ok {
			return id
		}
	}
	return 0
}

// SelfOnly 仅允许操作自己的资源（对比路径参数与当前登录用户）。
//
// 原路由里 DELETE /users/:id 和 PUT /users/profile 只校验了「是否登录」，
// 任何登录用户都能删除任意账号，属于越权漏洞。
// 引入角色体系前，先用归属校验兜住。
func SelfOnly(param string) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := c.Param(param)
		current := CurrentUserID(c)
		if target == "" || current == 0 || target != strconv.FormatUint(current, 10) {
			response.Error(c, errcode.ErrForbidden.WithDetails("只能操作自己的资源"))
			return
		}
		c.Next()
	}
}
