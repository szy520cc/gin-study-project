package middleware

import (
	"crypto/subtle"
	"net"
	"strings"
	"sync"

	"myproject/pkg/errcode"
	"myproject/pkg/logger"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// CtxServiceScope 服务接口鉴权级别在 gin.Context 中的 key。
const CtxServiceScope = "service_scope"

// 服务接口的两种权限级别。
const (
	// ScopeService 普通服务令牌：可求值「已发布指针」或「显式 version」。
	ScopeService = "service"
	// ScopeInternal 内部令牌：可额外使用 offline_flag 读取草稿（需 allow_offline_draft=true）。
	ScopeInternal = "internal"
)

// ServiceAuth 保护「供程序调用」的对外接口（当前仅 /engine/eval）。
//
// 鉴权模型（A 服务令牌为主 + B IP 白名单可选叠加）：
//   - A 服务令牌：调用方需出示 tokens 或 internalTokens 中的任意一个，取值来源为
//     X-Service-Token 头或 Authorization: Bearer。出示 internalTokens 的记为 internal 级别；
//   - B IP 白名单：一旦配置 allowCIDRs，调用方还必须来自这些网段 —— 与令牌是「叠加」关系，
//     即「令牌正确 且 IP 在白名单内」才放行。需配合 server.trusted_proxies 才能取到真实 IP。
//
// 三者全为空表示「未配置」：非生产环境打一次警告后放行（便于本地联调），
// 生产环境由 config.Validate 在启动阶段直接拒绝，正常不会走到这里。
func ServiceAuth(tokens, internalTokens, allowCIDRs []string) gin.HandlerFunc {
	all := make([]string, 0, len(tokens)+len(internalTokens))
	all = append(all, tokens...)
	all = append(all, internalTokens...)

	cidrs := parseCIDRs(allowCIDRs)
	needToken := len(all) > 0
	needCIDR := len(cidrs) > 0
	var warnOnce sync.Once

	return func(c *gin.Context) {
		if !needToken && !needCIDR {
			warnOnce.Do(func() {
				logger.C(c.Request.Context()).Warn("engine/eval 未配置鉴权（engine.auth 为空），当前放行；生产环境会拒绝启动")
			})
			c.Set(CtxServiceScope, ScopeService)
			c.Next()
			return
		}

		// B：IP 白名单（可选叠加，先判 IP 再验令牌）
		if needCIDR && !ipInCIDRs(c.ClientIP(), cidrs) {
			response.Error(c, errcode.ErrServiceIPForbidden)
			return
		}

		// A：服务令牌
		scope := ScopeService
		if needToken {
			presented := serviceTokenOf(c)
			if !matchAnyToken(presented, all) {
				response.Error(c, errcode.ErrServiceAuthRequired)
				return
			}
			if matchAnyToken(presented, internalTokens) {
				scope = ScopeInternal
			}
		}

		c.Set(CtxServiceScope, scope)
		l := logger.C(c.Request.Context()).With("service_scope", scope)
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context(), l))
		c.Next()
	}
}

// ServiceScopeOf 取服务接口鉴权级别；未经过 ServiceAuth 时返回空串。
func ServiceScopeOf(c *gin.Context) string {
	if v, ok := c.Get(CtxServiceScope); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// serviceTokenOf 从请求头取服务令牌：优先 X-Service-Token，其次 Authorization: Bearer。
func serviceTokenOf(c *gin.Context) string {
	if t := strings.TrimSpace(c.GetHeader("X-Service-Token")); t != "" {
		return t
	}
	parts := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
	if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// matchAnyToken 用常数时间比较判断 presented 是否命中令牌集合中的任意一个。
// 空令牌永不命中，避免「未配置令牌却因空串相等而放行」。
func matchAnyToken(presented string, candidates []string) bool {
	if presented == "" {
		return false
	}
	for _, t := range candidates {
		if t == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(t), []byte(presented)) == 1 {
			return true
		}
	}
	return false
}

// parseCIDRs 解析 IP / CIDR 字符串；非法项忽略（合法性由 config.Validate 在启动时保证）。
func parseCIDRs(list []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(list))
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(item); err == nil {
			out = append(out, ipnet)
			continue
		}
		if ip := net.ParseIP(item); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return out
}

func ipInCIDRs(ipStr string, cidrs []*net.IPNet) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	for _, n := range cidrs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
