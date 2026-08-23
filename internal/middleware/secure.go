package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders 补齐一组零成本的安全响应头。
//
// 这些头不解决业务漏洞，但能挡住一类「浏览器侧」的攻击面，
// 而且都是加一行 header 的成本：
//   - nosniff：禁止浏览器猜测 Content-Type（防止把 JSON 当 HTML 执行）
//   - DENY：禁止被 iframe 嵌套，防点击劫持
//   - Referrer-Policy：跨站跳转时不泄露完整 URL（URL 里常带 token/ID）
//   - CSP：API 只返回 JSON，直接禁掉所有资源加载
//   - Permissions-Policy：关掉不需要的浏览器能力
//
// hsts 仅在 HTTPS 下有意义（HTTP 响应里的 HSTS 会被浏览器忽略），
// 且一旦下发，浏览器会在 max-age 内强制走 HTTPS —— 证书没配好就会全站不可用，
// 因此默认关闭，由生产配置显式开启。
func SecurityHeaders(hsts bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
