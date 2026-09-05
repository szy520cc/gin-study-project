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
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		// Admin 页面（AMIS 低代码渲染）：
		//   - AMIS 内部用 new Function 动态编译表达式/模板，必须开 unsafe-eval
		//   - 内联的 <script>/<style> 与 React 运行时内联样式需要 unsafe-inline
		//   - 图标、字体走 data:，外部资源走 https:
		// 这里只对 /admin 放开；API 路由仍保持 default-src 'none'。
		if path := c.Request.URL.Path; len(path) >= 6 && path[:6] == "/admin" {
			h.Set("Content-Security-Policy",
				"default-src 'self' https:; "+
					"script-src 'self' 'unsafe-inline' 'unsafe-eval' https:; "+
					"style-src 'self' 'unsafe-inline' https:; "+
					"img-src 'self' data: https:; "+
					"font-src 'self' data: https:; "+
					"connect-src 'self' https:; "+
					"frame-ancestors 'none'")
		} else {
			// API 路由只返回 JSON，直接禁掉所有资源加载
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		}

		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
