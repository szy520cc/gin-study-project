package module_test

import (
	"testing"

	"myproject/internal/module"

	"github.com/gin-gonic/gin"
)

// TestAllRegistered 验证 All 清单里每个模块的路由都能正常挂上。
// 漏登记、路径写错、模块间路径冲突（gin 会 panic），都会在这里暴露。
func TestAllRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mods := module.All
	if len(mods) == 0 {
		t.Fatal("module.All 为空")
	}

	noop := func(*gin.Context) {}
	r := gin.New()
	g := r.Group("/api/v1")
	for _, register := range mods {
		register(g, module.Deps{Auth: noop, AuthLimit: noop})
	}

	// 每个模块至少要挂上自己的路由，顺带确认路由声明本身不 panic
	got := make(map[string]bool)
	for _, ri := range r.Routes() {
		got[ri.Method+" "+ri.Path] = true
	}
	for _, want := range []string{
		"POST /api/v1/users/login",
		"GET /api/v1/users/:id",
		"POST /api/v1/orders",
		"PUT /api/v1/orders/:id/status",
	} {
		if !got[want] {
			t.Errorf("路由未注册: %s", want)
		}
	}
}
