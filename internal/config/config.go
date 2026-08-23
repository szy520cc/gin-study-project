package config

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 应用配置
type Config struct {
	// Env 部署环境，由启动参数 -env / APP_ENV 决定，取值 dev / test / prod。
	// 不是 yaml 字段：它决定「加载哪些文件、要不要跑生产强校验」，
	// 一旦允许被配置文件或环境变量覆盖，生产校验就能被一行配置绕开。
	Env string `mapstructure:"-"`

	Server    ServerConfig    `mapstructure:"server"`
	Admin     AdminConfig     `mapstructure:"admin"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Log       LogConfig       `mapstructure:"log"`
	JWT       JWTConfig       `mapstructure:"jwt"`
	CORS      CORSConfig      `mapstructure:"cors"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
}

// 合法的部署环境。白名单化的理由见 Load：
// 拼错成 production 时原实现只是「跳过不存在的文件」，
// server.mode 保持默认 debug，于是整段生产校验静默不执行。
const (
	EnvDev  = "dev"
	EnvTest = "test"
	EnvProd = "prod"
)

var validEnvs = []string{EnvDev, EnvTest, EnvProd}

// AdminConfig 内部管理端口配置（指标、pprof、版本信息）。
//
// 与业务端口分离：/metrics 暴露路由清单与流量特征，/debug/pprof 能拉堆和 CPU profile，
// 都属于内部信息，且 profile 可被反复触发当成 DoS。默认只监听回环地址。
type AdminConfig struct {
	Addr  string `mapstructure:"addr"`  // 为空则不启动 admin 服务
	Pprof bool   `mapstructure:"pprof"` // 是否开启 /debug/pprof
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Addr              string `mapstructure:"addr"`
	Mode              string `mapstructure:"mode"`                // debug, release, test
	ReadTimeout       int    `mapstructure:"read_timeout"`        // 秒
	WriteTimeout      int    `mapstructure:"write_timeout"`       // 秒
	IdleTimeout       int    `mapstructure:"idle_timeout"`        // 秒
	ReadHeaderTimeout int    `mapstructure:"read_header_timeout"` // 秒
	RequestTimeout    int    `mapstructure:"request_timeout"`     // 单请求处理超时（秒），0 表示不限制
	ShutdownTimeout   int    `mapstructure:"shutdown_timeout"`    // 优雅退出等待（秒）
	MaxHeaderBytes    int    `mapstructure:"max_header_bytes"`
	// MaxBodyBytes 请求体大小上限（字节）。MaxHeaderBytes 只管 header，
	// body 不设限时一个大 JSON 就能把进程内存打满。0 表示不限制（不建议）。
	MaxBodyBytes int64 `mapstructure:"max_body_bytes"`
	// TrustedProxies 可信代理网段。
	// gin 默认信任所有代理，ClientIP() 会取 X-Forwarded-For 首段 ——
	// 攻击者每个请求伪造一个 IP 就能绕过按 IP 的限流，日志里的 IP 也全是假的。
	// 直连部署留空（只信任 RemoteAddr）；有网关时填网关网段，如 ["10.0.0.0/8"]。
	TrustedProxies []string `mapstructure:"trusted_proxies"`
	// DrainDelay 收到 SIGTERM 后，先让 /readyz 返回 503 并等待这段时间，
	// 给负载均衡摘流的机会，再开始关闭服务（秒）。
	// 设为 0 会导致摘流窗口内的请求被直接拒绝。
	DrainDelay int `mapstructure:"drain_delay"`
	// EnableHSTS 是否下发 Strict-Transport-Security。
	// 仅 HTTPS 有意义，且一旦下发浏览器会在 max-age 内强制 HTTPS，
	// 证书没配好会导致全站不可用 —— 因此默认关闭。
	EnableHSTS bool `mapstructure:"enable_hsts"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Driver          string `mapstructure:"driver"`
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	Username        string `mapstructure:"username"`
	Password        string `mapstructure:"password"`
	DBName          string `mapstructure:"dbname"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"` // 分钟
	LogLevel        string `mapstructure:"log_level"`         // silent, error, warn, info
	SlowThreshold   int    `mapstructure:"slow_threshold"`    // 慢查询阈值（毫秒）
	// LogSQLParams 是否把 SQL 绑定参数打进日志。与 log_level 解耦：
	// 排查问题把级别调成 info 时，不该顺带把邮箱、手机号、口令哈希写进日志。
	LogSQLParams bool `mapstructure:"log_sql_params"`
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level     string `mapstructure:"level"`      // debug, info, warn, error
	Format    string `mapstructure:"format"`     // json, console
	FilePath  string `mapstructure:"file_path"`  // 日志目录，为空则仅输出到 stdout
	LogBody   bool   `mapstructure:"log_body"`   // 是否记录请求/响应体（会带来内存拷贝开销）
	AddSource bool   `mapstructure:"add_source"` // 是否记录调用位置
	// 以下三项是保留策略。只按天切分而不清理，跑几个月就会把磁盘写满，
	// 而磁盘满会连带拖垮数据库和整机。
	MaxSizeMB  int `mapstructure:"max_size_mb"`  // 单文件上限，超过则切分，0 表示不限制
	MaxBackups int `mapstructure:"max_backups"`  // 保留的历史文件数，0 表示不限制
	MaxAgeDays int `mapstructure:"max_age_days"` // 历史文件保留天数，0 表示不限制
}

// JWTConfig JWT 配置
type JWTConfig struct {
	Secret     string `mapstructure:"secret"`
	ExpireTime int    `mapstructure:"expire_time"` // 小时
	Issuer     string `mapstructure:"issuer"`
}

// CORSConfig 跨域配置
type CORSConfig struct {
	// AllowOrigins 允许的来源白名单。含 "*" 时按「允许全部但不带凭证」处理，
	// 因为浏览器禁止 Access-Control-Allow-Origin:* 与 Allow-Credentials:true 并存。
	AllowOrigins     []string `mapstructure:"allow_origins"`
	AllowCredentials bool     `mapstructure:"allow_credentials"`
	MaxAge           int      `mapstructure:"max_age"` // 小时
}

// RateLimitConfig 限流配置（单机维度，按客户端 IP）
type RateLimitConfig struct {
	Enabled bool    `mapstructure:"enabled"`
	RPS     float64 `mapstructure:"rps"`
	Burst   int     `mapstructure:"burst"`
	// 认证类接口（注册/登录）单独的更严配额。
	// bcrypt 每次校验约 60-100ms CPU，登录接口天然是 CPU 放大器：
	// 几十个并发就能打满 CPU，必须比普通接口限得更死。
	AuthRPS   float64 `mapstructure:"auth_rps"`
	AuthBurst int     `mapstructure:"auth_burst"`
}

// IsProd 是否生产环境。
// 依据是部署环境 Env（启动参数 -env / APP_ENV），不是 server.mode ——
// mode 可被 APP_SERVER_MODE 覆盖，用它判定会让生产强校验和
// migrate 的 -drop 保护被一个环境变量解除。
func (c *Config) IsProd() bool {
	return c.Env == EnvProd
}

// Duration 辅助方法：把秒/分钟配置转成 time.Duration
func (s ServerConfig) ReadTimeoutDuration() time.Duration       { return sec(s.ReadTimeout) }
func (s ServerConfig) WriteTimeoutDuration() time.Duration      { return sec(s.WriteTimeout) }
func (s ServerConfig) IdleTimeoutDuration() time.Duration       { return sec(s.IdleTimeout) }
func (s ServerConfig) ReadHeaderTimeoutDuration() time.Duration { return sec(s.ReadHeaderTimeout) }
func (s ServerConfig) RequestTimeoutDuration() time.Duration    { return sec(s.RequestTimeout) }
func (s ServerConfig) ShutdownTimeoutDuration() time.Duration   { return sec(s.ShutdownTimeout) }
func (s ServerConfig) DrainDelayDuration() time.Duration        { return sec(s.DrainDelay) }

func sec(v int) time.Duration { return time.Duration(v) * time.Second }

// setDefaults 默认值集中声明，配置文件只需覆盖差异项
func setDefaults(v *viper.Viper) {
	v.SetDefault("server.addr", ":8080")
	v.SetDefault("server.mode", "debug")
	v.SetDefault("server.read_timeout", 10)
	v.SetDefault("server.write_timeout", 10)
	v.SetDefault("server.idle_timeout", 60)
	v.SetDefault("server.read_header_timeout", 5)
	v.SetDefault("server.request_timeout", 10)
	v.SetDefault("server.shutdown_timeout", 15)
	v.SetDefault("server.max_header_bytes", 1<<20)
	v.SetDefault("server.max_body_bytes", 1<<20)
	v.SetDefault("server.trusted_proxies", []string{})
	v.SetDefault("server.drain_delay", 5)
	v.SetDefault("server.enable_hsts", false)

	// admin 端口只监听回环：/metrics 与 pprof 属于内部信息，不应对外暴露
	v.SetDefault("admin.addr", "127.0.0.1:9090")
	v.SetDefault("admin.pprof", true)

	v.SetDefault("database.driver", "mysql")
	v.SetDefault("database.port", 3306)
	v.SetDefault("database.max_idle_conns", 10)
	v.SetDefault("database.max_open_conns", 100)
	v.SetDefault("database.conn_max_lifetime", 60)
	v.SetDefault("database.log_level", "warn")
	v.SetDefault("database.slow_threshold", 200)
	// SQL 绑定参数默认不落盘：里面是邮箱、手机号、口令哈希这类数据
	v.SetDefault("database.log_sql_params", false)

	v.SetDefault("redis.enabled", true)
	// 显式给出默认 host：留空时 go-redis 的 Addr 会是 ":6379"，
	// 看起来「没配」实际连的是本机，排查时很容易被误导
	v.SetDefault("redis.host", "127.0.0.1")
	v.SetDefault("redis.port", 6379)
	v.SetDefault("redis.db", 0)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("log.log_body", false)
	v.SetDefault("log.add_source", false)
	v.SetDefault("log.max_size_mb", 100)
	v.SetDefault("log.max_backups", 14)
	v.SetDefault("log.max_age_days", 30)

	v.SetDefault("jwt.expire_time", 24)
	v.SetDefault("jwt.issuer", "myproject")

	v.SetDefault("cors.allow_origins", []string{"*"})
	v.SetDefault("cors.allow_credentials", false)
	v.SetDefault("cors.max_age", 12)

	// 限流默认开启：关闭状态下登录接口（bcrypt）可被少量并发打满 CPU
	v.SetDefault("rate_limit.enabled", true)
	v.SetDefault("rate_limit.rps", 50)
	v.SetDefault("rate_limit.burst", 100)
	v.SetDefault("rate_limit.auth_rps", 1)
	v.SetDefault("rate_limit.auth_burst", 5)

	// 以下 key 允许「只从环境变量注入」，因此必须在这里登记一个空默认值。
	// 原因：BindEnv 只能作用于已知的 key，而 v.AllKeys() 不包含
	// 既没有默认值、也没出现在任何配置文件里的 key ——
	// 不登记的话，APP_DATABASE_PASSWORD 这类注入会被静默忽略，
	// 而这几个恰恰是「刻意不写进入库配置文件」的敏感项。
	for _, key := range envOnlyKeys {
		v.SetDefault(key, "")
	}
}

// envOnlyKeys 可能只通过环境变量提供的配置项。
//
// 注意：这里登记的 key 会被赋一个空默认值，所以**不要**把已经有真实默认值的
// key 放进来（例如 redis.host 默认 127.0.0.1），否则空值会把默认值覆盖掉。
// 有默认值的 key 本身就已经出现在 AllKeys 里，BindEnv 照样生效。
var envOnlyKeys = []string{
	"database.host",
	"database.username",
	"database.password",
	"database.dbname",
	"redis.password",
	"jwt.secret",
	"log.file_path",
}

// Load 加载配置。优先级（低到高）：
//  1. setDefaults 内置默认值
//  2. config.yaml
//  3. config.<env>.yaml（不存在则跳过）
//  4. config.local.yaml（本地覆盖，已被 .gitignore 忽略，用于放本机凭据）
//  5. 环境变量 APP_XXX_YYY（如 APP_DATABASE_PASSWORD、APP_JWT_SECRET）
//
// 敏感信息（数据库密码、Redis 密码、JWT secret）不应写入被版本管理的配置文件，
// 生产环境请通过环境变量或密钥管理系统注入。
func Load(path string, env string) (*Config, error) {
	// env 白名单化：拼错成 production / prd 时，原实现只是「跳过不存在的文件」，
	// server.mode 保持默认 debug，IsProd() 为 false ——
	// 生产强校验、pprof 关闭、CORS 白名单、错误细节屏蔽全部静默失效。
	env = strings.ToLower(strings.TrimSpace(env))
	if env == "" {
		env = EnvDev
	}
	if !isValidEnv(env) {
		return nil, fmt.Errorf("非法的运行环境 %q（可选 %s）", env, strings.Join(validEnvs, "/"))
	}

	v := viper.New()
	v.SetConfigType("yaml")
	v.AddConfigPath(path)
	setDefaults(v)

	// 环境变量绑定：APP_ 前缀，嵌套 key 用下划线连接
	v.SetEnvPrefix("APP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 主配置必须存在
	v.SetConfigName("config")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取默认配置文件失败: %w", err)
	}
	loaded := []string{"config.yaml"}

	// 环境配置：生产必须存在，其余环境允许缺省。
	//
	// 生产不允许缺省的理由是「静默按默认值跑」的后果太重：
	// config.prod.yaml 一旦没打进部署包，mode 会是 debug、pprof 开着、
	// CORS 是 *、错误细节对外暴露，而进程照样起来。
	v.SetConfigName("config." + env)
	if err := v.MergeInConfig(); err != nil {
		if env == EnvProd {
			return nil, fmt.Errorf("读取环境配置 config.%s.yaml 失败: %w", env, err)
		}
		bootLog("[WARN] 未找到 config.%s.yaml，将使用默认配置", env)
	} else {
		loaded = append(loaded, "config."+env+".yaml")
	}

	// 本地覆盖只在非生产生效。
	// config.local.yaml 里放的是本机凭据，优先级又高于环境配置 ——
	// 一旦它随 configs/ 目录被同步到生产机，会静默把生产的库地址和
	// JWT secret 换成开发用的那一套。
	if env != EnvProd {
		v.SetConfigName("config.local")
		if err := v.MergeInConfig(); err == nil {
			loaded = append(loaded, "config.local.yaml")
		}
	}

	// AutomaticEnv 对 Unmarshal 路径不生效，需为每个 key 显式 BindEnv
	for _, key := range v.AllKeys() {
		_ = v.BindEnv(key)
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	cfg.Env = env

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	bootLog("配置加载完成 env=%s files=%s", env, strings.Join(loaded, ","))
	return cfg, nil
}

func isValidEnv(env string) bool {
	for _, e := range validEnvs {
		if e == env {
			return true
		}
	}
	return false
}

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
	if c.Log.MaxSizeMB < 0 || c.Log.MaxBackups < 0 || c.Log.MaxAgeDays < 0 {
		errs = append(errs, "log.max_size_mb/max_backups/max_age_days 不能为负（负值会让保留策略静默失效）")
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

// bootLog 启动阶段日志：此时主日志系统尚未初始化
func bootLog(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, "%s [boot] "+format+"\n",
		append([]interface{}{time.Now().Format(time.RFC3339)}, args...)...)
}
