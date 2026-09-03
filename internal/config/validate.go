package config

import (
	"fmt"
	"net"
	"strings"
)

// 本文件是启动校验：Validate 与它的判定助手。
// 与 config.go 分开是因为「配置怎么加载」和「什么样的配置算合法」
// 是两件独立的事，出问题时也总是只看其中一件。

// Validate 启动即校验：缺失或明显不安全的配置直接拒绝启动，
// 避免「用占位符密钥跑在生产」这类问题被推迟到线上才发现。
func (c *Config) Validate() error {
	var errs []string

	if c.Server.Addr == "" {
		errs = append(errs, "server.addr 不能为空")
	}
	switch c.Server.Mode {
	case "debug", "release", "test":
	default:
		errs = append(errs, fmt.Sprintf("server.mode 非法: %q（可选 debug/release/test）", c.Server.Mode))
	}

	if c.Database.Host == "" || c.Database.DBName == "" || c.Database.Username == "" {
		errs = append(errs, "database.host/dbname/username 不能为空")
	}
	if !isOneOf(c.Database.LogLevel, "silent", "error", "warn", "info") {
		errs = append(errs, fmt.Sprintf("database.log_level 非法: %q（可选 silent/error/warn/info）", c.Database.LogLevel))
	}

	// 以下几项的共同点：配错不会报错，只会「静默失效」。
	// 校验的意义就是把「看起来开着、其实没生效」变成启动失败。
	if c.Redis.Enabled && c.Redis.Host == "" {
		errs = append(errs, "redis.enabled=true 时 redis.host 不能为空（否则会连到本机 6379）")
	}
	if !isOneOf(c.Log.Level, "debug", "info", "warn", "error") {
		errs = append(errs, fmt.Sprintf("log.level 非法: %q（可选 debug/info/warn/error）", c.Log.Level))
	}
	if !isOneOf(c.Log.Format, "json", "console") {
		errs = append(errs, fmt.Sprintf("log.format 非法: %q（可选 json/console）", c.Log.Format))
	}
	if c.Log.MaxBackups < 0 || c.Log.MaxAgeDays < 0 {
		errs = append(errs, "log.max_backups/max_age_days 不能为负（负值会让保留策略静默失效）")
	}
	if c.RateLimit.Enabled {
		if c.RateLimit.RPS <= 0 || c.RateLimit.Burst <= 0 {
			errs = append(errs, "rate_limit.enabled=true 时 rps/burst 必须大于 0（否则限流被静默跳过）")
		}
		if c.RateLimit.AuthRPS <= 0 || c.RateLimit.AuthBurst <= 0 {
			errs = append(errs, "rate_limit.enabled=true 时 auth_rps/auth_burst 必须大于 0（登录接口会失去保护）")
		}
	}
	if c.JWT.ExpireTime <= 0 {
		errs = append(errs, "jwt.expire_time 必须大于 0（为 0 时签发的 token 立即过期）")
	}
	if c.Server.ShutdownTimeout <= 0 {
		errs = append(errs, "server.shutdown_timeout 必须大于 0（为 0 时会立即强杀存量请求）")
	}
	if c.Server.MaxBodyBytes <= 0 {
		errs = append(errs, "server.max_body_bytes 必须大于 0（为 0 时请求体不设限）")
	}
	if c.Server.RequestTimeout <= 0 {
		errs = append(errs, "server.request_timeout 必须大于 0（为 0 时单请求不设超时）")
	}

	for _, cidr := range c.Server.TrustedProxies {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			if net.ParseIP(cidr) == nil {
				errs = append(errs, fmt.Sprintf("server.trusted_proxies 含非法条目 %q（需为 IP 或 CIDR）", cidr))
			}
		}
	}

	if c.JWT.Secret == "" {
		errs = append(errs, "jwt.secret 不能为空（请通过环境变量 APP_JWT_SECRET 注入）")
	}
	for _, weak := range weakSecrets {
		if strings.EqualFold(c.JWT.Secret, weak) {
			errs = append(errs, fmt.Sprintf("jwt.secret 使用了占位符 %q，必须替换", weak))
		}
	}
	if c.IsProd() {
		// mode 可被 APP_SERVER_MODE 覆盖，config.prod.yaml 里写了 release 也拦不住。
		// debug 模式下 gin 会打印整张路由表和框架 debug 日志。
		if c.Server.Mode != "release" {
			errs = append(errs, fmt.Sprintf("生产环境 server.mode 必须为 release，当前为 %q", c.Server.Mode))
		}
		if len(c.JWT.Secret) < 32 {
			errs = append(errs, "生产环境 jwt.secret 长度必须 >= 32")
		}
		if c.Database.Password == "" {
			errs = append(errs, "生产环境 database.password 不能为空")
		}
		// 本项目用 Bearer token 认证，不依赖 cookie：即使 allow_credentials=false，
		// allow_origins=* 也意味着任意站点的 JS 带上受害者 token 就能读到响应体。
		// 所以这里不是「建议」而是直接拒绝启动。
		if containsWildcard(c.CORS.AllowOrigins) {
			errs = append(errs, "生产环境 cors.allow_origins 不能包含 *，必须是显式白名单")
		}
		if c.CORS.AllowCredentials && containsWildcard(c.CORS.AllowOrigins) {
			errs = append(errs, "cors.allow_origins 含 * 时不能开启 allow_credentials（浏览器会拒绝）")
		}
		// pprof 能拉堆和 CPU profile：堆里有 token、SQL 明文，
		// /profile 还能被反复触发当成 DoS。绑在非回环地址上等于对内网开放。
		if c.Admin.Pprof && c.Admin.Addr != "" && !isLoopbackAddr(c.Admin.Addr) {
			errs = append(errs, fmt.Sprintf("生产环境 admin.pprof=true 时 admin.addr 必须监听回环地址，当前为 %q", c.Admin.Addr))
		}
		if c.Database.LogSQLParams {
			errs = append(errs, "生产环境不允许 database.log_sql_params=true（SQL 绑定参数会连同用户数据落盘）")
		}
		// 不做成硬失败：直连暴露的部署确实应该保持 trusted_proxies 为空，
		// 配置里无法区分「直连」和「忘填」。但后果足够严重，必须显式提示。
		if c.RateLimit.Enabled && len(c.Server.TrustedProxies) == 0 {
			bootLog("[WARN] server.trusted_proxies 为空且已开启限流：若本实例在 LB/网关后面，"+
				"ClientIP() 恒为网关地址，按 IP 的限流会退化成全站共用一个桶（rps=%v 即整实例上限）", c.RateLimit.RPS)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("配置校验失败:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

var weakSecrets = []string{
	"your-secret-key-here",
	"secret",
	"changeme",
	"123456",
}

func containsWildcard(origins []string) bool {
	for _, o := range origins {
		if o == "*" {
			return true
		}
	}
	return false
}

func isOneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// isLoopbackAddr 判断 "host:port" 里的 host 是否为回环地址。
// 空 host（如 ":9090"）等价于监听全部网卡，按「非回环」处理。
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
