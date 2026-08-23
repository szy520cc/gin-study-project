package middleware

import (
	"time"

	"myproject/internal/config"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Cors 跨域中间件。
//
// 原实现把 AllowOrigins 写死成 ["*"] 且同时开启 AllowCredentials，
// 这是浏览器明确禁止的组合（Access-Control-Allow-Origin:* 不能与
// Access-Control-Allow-Credentials:true 并存），带 cookie 的跨域请求会被拒绝。
// 现在改为从配置读取白名单，并在检测到该冲突时自动降级，避免线上出现静默失效。
func Cors(cfg config.CORSConfig) gin.HandlerFunc {
	allowAll := false
	origins := make([]string, 0, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		if o == "*" {
			allowAll = true
			continue
		}
		origins = append(origins, o)
	}

	c := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", RequestIDHeader},
		ExposeHeaders:    []string{"Content-Length", "Content-Type", RequestIDHeader},
		AllowCredentials: cfg.AllowCredentials,
		MaxAge:           time.Duration(cfg.MaxAge) * time.Hour,
	}

	switch {
	case allowAll && cfg.AllowCredentials:
		// 冲突组合：保留白名单语义，忽略 *，否则浏览器会直接拒绝
		c.AllowOrigins = origins
		if len(origins) == 0 {
			// 白名单为空则退化为不允许携带凭证的全放开
			c.AllowAllOrigins = true
			c.AllowCredentials = false
		}
	case allowAll:
		c.AllowAllOrigins = true
	default:
		c.AllowOrigins = origins
	}

	return cors.New(c)
}
