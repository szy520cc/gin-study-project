package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 配置是最容易「配了但没生效」的地方：默认值、环境变量覆盖、启动校验
// 三者任一失效都不会报错，只会在线上表现为奇怪的行为。这里逐项锁住。

const minimalYAML = `
database:
  host: "127.0.0.1"
  username: "root"
  dbname: "gin"
jwt:
  secret: "0123456789abcdef0123456789abcdef"
`

func writeConfig(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoad_AppliesDefaults(t *testing.T) {
	dir := writeConfig(t, map[string]string{"config.yaml": minimalYAML})

	cfg, err := Load(dir, "dev")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	if cfg.Server.Addr != ":8080" {
		t.Errorf("server.addr 默认值应为 :8080，实际 %q", cfg.Server.Addr)
	}
	if cfg.Server.MaxBodyBytes != 1<<20 {
		t.Errorf("max_body_bytes 默认值应为 1MB，实际 %d", cfg.Server.MaxBodyBytes)
	}
	// 限流默认必须开启：关闭时登录接口（bcrypt）可被少量并发打满 CPU
	if !cfg.RateLimit.Enabled {
		t.Error("rate_limit.enabled 默认应为 true")
	}
	if cfg.RateLimit.AuthBurst == 0 {
		t.Error("认证接口应有独立的限流配额默认值")
	}
	// admin 默认只监听回环：/metrics 与 pprof 不应对外暴露
	if !strings.HasPrefix(cfg.Admin.Addr, "127.0.0.1") {
		t.Errorf("admin.addr 默认应只监听回环，实际 %q", cfg.Admin.Addr)
	}
	if cfg.Log.MaxBackups == 0 || cfg.Log.MaxAgeDays == 0 {
		t.Error("日志保留策略应有默认值，否则磁盘会被写满")
	}
}

// TestLoad_EnvOverridesNestedKey 锁住环境变量覆盖。
// viper 的 AutomaticEnv 对「嵌套 key + Unmarshal」不生效，
// 必须对每个 key 显式 BindEnv —— 这条以前是坏的：注释说支持 env 覆盖，
// 实际上 APP_DATABASE_PASSWORD 根本读不到，只能把密码写回配置文件。
func TestLoad_EnvOverridesNestedKey(t *testing.T) {
	dir := writeConfig(t, map[string]string{"config.yaml": minimalYAML})

	t.Setenv("APP_DATABASE_PASSWORD", "from-env")
	t.Setenv("APP_SERVER_ADDR", ":9999")

	cfg, err := Load(dir, "dev")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	if cfg.Database.Password != "from-env" {
		t.Errorf("database.password 应被环境变量覆盖，实际 %q", cfg.Database.Password)
	}
	if cfg.Server.Addr != ":9999" {
		t.Errorf("server.addr 应被环境变量覆盖，实际 %q", cfg.Server.Addr)
	}
}

func TestLoad_EnvConfigOverridesBase(t *testing.T) {
	dir := writeConfig(t, map[string]string{
		"config.yaml": `
database:
  host: "127.0.0.1"
  username: "root"
  password: "p"
  dbname: "gin"
jwt:
  secret: "0123456789abcdef0123456789abcdef"
`,
		"config.prod.yaml": "server:\n  mode: \"release\"\n  addr: \":9090\"\ncors:\n  allow_origins: [\"https://app.example.com\"]\n",
	})

	cfg, err := Load(dir, "prod")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	if !cfg.IsProd() {
		t.Error("env=prod 时 IsProd 应为 true")
	}
	if cfg.Server.Mode != "release" {
		t.Errorf("config.prod.yaml 应覆盖 server.mode，实际 %q", cfg.Server.Mode)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("环境配置应覆盖默认值，实际 %q", cfg.Server.Addr)
	}
}

// TestLoad_RejectsUnknownEnv 环境名必须白名单化。
// 拼错成 production 时，原实现只是「跳过不存在的 config.production.yaml」，
// 于是 mode 保持默认 debug、IsProd() 为 false，整段生产强校验静默不执行。
func TestLoad_RejectsUnknownEnv(t *testing.T) {
	dir := writeConfig(t, map[string]string{"config.yaml": minimalYAML})

	_, err := Load(dir, "production")
	if err == nil || !strings.Contains(err.Error(), "非法的运行环境") {
		t.Errorf("非法环境名应拒绝启动，实际: %v", err)
	}
}

// TestLoad_ProdRequiresEnvFile 生产必须有 config.prod.yaml。
// 它没打进部署包时，进程会按默认值跑（debug 模式、pprof 开着、CORS 为 *），
// 而且照样能起来 —— 这类问题只能在启动时拦。
func TestLoad_ProdRequiresEnvFile(t *testing.T) {
	dir := writeConfig(t, map[string]string{"config.yaml": minimalYAML})

	_, err := Load(dir, "prod")
	if err == nil || !strings.Contains(err.Error(), "config.prod.yaml") {
		t.Errorf("生产缺少环境配置应拒绝启动，实际: %v", err)
	}
}

// TestLoad_SkipsLocalOverrideInProd config.local.yaml 的优先级高于环境配置，
// 一旦随 configs/ 目录被同步到生产机，会静默把库地址和 secret 换成开发的那套。
func TestLoad_SkipsLocalOverrideInProd(t *testing.T) {
	files := map[string]string{
		"config.yaml": "database:\n  host: \"prod-host\"\n  username: u\n  dbname: d\n  password: p\n" +
			"jwt:\n  secret: \"0123456789abcdef0123456789abcdef\"\n",
		"config.prod.yaml":  "server:\n  mode: \"release\"\ncors:\n  allow_origins: [\"https://app.example.com\"]\n",
		"config.local.yaml": "database:\n  host: \"local-host\"\n",
	}

	prod, err := Load(writeConfig(t, files), "prod")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if prod.Database.Host != "prod-host" {
		t.Errorf("生产不应加载 config.local.yaml，实际 host=%q", prod.Database.Host)
	}

	dev, err := Load(writeConfig(t, files), "dev")
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if dev.Database.Host != "local-host" {
		t.Errorf("非生产应加载 config.local.yaml，实际 host=%q", dev.Database.Host)
	}
}

func TestValidate_RejectsBadConfig(t *testing.T) {
	cases := map[string]struct {
		yaml string
		want string
	}{
		"jwt secret 为空": {
			yaml: "database:\n  host: h\n  username: u\n  dbname: d\n",
			want: "jwt.secret",
		},
		"jwt secret 是占位符": {
			yaml: "database:\n  host: h\n  username: u\n  dbname: d\njwt:\n  secret: \"your-secret-key-here\"\n",
			want: "占位符",
		},
		"数据库信息缺失": {
			yaml: "jwt:\n  secret: \"0123456789abcdef0123456789abcdef\"\n",
			want: "database.host",
		},
		"server.mode 非法": {
			yaml: minimalYAML + "server:\n  mode: \"production\"\n",
			want: "server.mode",
		},
		"trusted_proxies 非法": {
			yaml: minimalYAML + "server:\n  trusted_proxies: [\"not-an-ip\"]\n",
			want: "trusted_proxies",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeConfig(t, map[string]string{"config.yaml": tc.yaml})

			_, err := Load(dir, "dev")
			if err == nil {
				t.Fatal("非法配置应拒绝启动")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误信息应包含 %q，实际: %v", tc.want, err)
			}
		})
	}
}

// TestValidate_ProdRules 生产环境的额外约束。
// 用占位符或弱密钥跑在生产，等于认证形同虚设 —— 这类问题必须在启动时拦住，
// 而不是推迟到线上被人发现。
func TestValidate_ProdRules(t *testing.T) {
	base := "database:\n  host: h\n  username: u\n  dbname: d\n  password: p\nserver:\n  mode: \"release\"\n"

	t.Run("生产 jwt secret 长度不足", func(t *testing.T) {
		dir := writeConfig(t, map[string]string{
			"config.yaml":      base + "jwt:\n  secret: \"short-secret\"\n",
			"config.prod.yaml": "# 生产环境必须存在这个文件\n",
		})

		if _, err := Load(dir, "prod"); err == nil || !strings.Contains(err.Error(), "32") {
			t.Errorf("生产应要求 secret >= 32 位，实际: %v", err)
		}
	})

	t.Run("生产数据库密码为空", func(t *testing.T) {
		dir := writeConfig(t, map[string]string{
			"config.yaml": "database:\n  host: h\n  username: u\n  dbname: d\nserver:\n  mode: \"release\"\n" +
				"jwt:\n  secret: \"0123456789abcdef0123456789abcdef\"\n",
			"config.prod.yaml": "# 生产环境必须存在这个文件\n",
		})

		if _, err := Load(dir, "prod"); err == nil || !strings.Contains(err.Error(), "password") {
			t.Errorf("生产应要求数据库密码非空，实际: %v", err)
		}
	})

	t.Run("CORS 通配符与凭证不能并存", func(t *testing.T) {
		dir := writeConfig(t, map[string]string{
			"config.yaml": base + "jwt:\n  secret: \"0123456789abcdef0123456789abcdef\"\n" +
				"cors:\n  allow_origins: [\"*\"]\n  allow_credentials: true\n",
			"config.prod.yaml": "# 生产环境必须存在这个文件\n",
		})

		if _, err := Load(dir, "prod"); err == nil || !strings.Contains(err.Error(), "allow_credentials") {
			t.Errorf("浏览器禁止该组合，应在启动时拦住，实际: %v", err)
		}
	})

	t.Run("生产开 pprof 但监听非回环", func(t *testing.T) {
		dir := writeConfig(t, map[string]string{
			"config.yaml": base + "jwt:\n  secret: \"0123456789abcdef0123456789abcdef\"\n" +
				"admin:\n  addr: \"0.0.0.0:9090\"\n  pprof: true\n",
			"config.prod.yaml": "# 生产环境必须存在这个文件\n",
		})

		if _, err := Load(dir, "prod"); err == nil || !strings.Contains(err.Error(), "pprof") {
			t.Errorf("生产 pprof 绑非回环应被拦住，实际: %v", err)
		}
	})
}
