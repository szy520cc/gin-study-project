package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// callServiceAuth 起一个只挂 ServiceAuth 的最小路由，返回 (状态码, ctx 里的 scope)。
func callServiceAuth(tokens, internal, cidrs []string, headerKey, headerVal, remoteAddr string) (int, string) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var scope string
	r.POST("/engine/eval", ServiceAuth(tokens, internal, cidrs), func(c *gin.Context) {
		scope = ServiceScopeOf(c)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/engine/eval", nil)
	if headerKey != "" {
		req.Header.Set(headerKey, headerVal)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, scope
}

func TestServiceAuth_UnconfiguredAllows(t *testing.T) {
	code, scope := callServiceAuth(nil, nil, nil, "", "", "203.0.113.9:1234")
	if code != http.StatusOK {
		t.Fatalf("未配置鉴权时应放行（非生产），实际 %d", code)
	}
	if scope != ScopeService {
		t.Errorf("未配置时应记为 service 级别，实际 %q", scope)
	}
}

func TestServiceAuth_TokenViaHeader(t *testing.T) {
	code, scope := callServiceAuth([]string{"secret-1"}, nil, nil, "X-Service-Token", "secret-1", "")
	if code != http.StatusOK || scope != ScopeService {
		t.Fatalf("普通令牌应放行且为 service，实际 code=%d scope=%q", code, scope)
	}
}

func TestServiceAuth_TokenViaBearer(t *testing.T) {
	code, scope := callServiceAuth([]string{"secret-1"}, nil, nil, "Authorization", "Bearer secret-1", "")
	if code != http.StatusOK || scope != ScopeService {
		t.Fatalf("Bearer 形式应放行，实际 code=%d scope=%q", code, scope)
	}
}

func TestServiceAuth_InternalTokenScope(t *testing.T) {
	code, scope := callServiceAuth([]string{"svc"}, []string{"inner"}, nil, "X-Service-Token", "inner", "")
	if code != http.StatusOK || scope != ScopeInternal {
		t.Fatalf("内部令牌应放行且为 internal，实际 code=%d scope=%q", code, scope)
	}
}

func TestServiceAuth_WrongTokenRejected(t *testing.T) {
	code, _ := callServiceAuth([]string{"secret-1"}, nil, nil, "X-Service-Token", "wrong", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("错误令牌应 401，实际 %d", code)
	}
}

func TestServiceAuth_MissingTokenRejected(t *testing.T) {
	code, _ := callServiceAuth([]string{"secret-1"}, nil, nil, "", "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("缺少令牌应 401，实际 %d", code)
	}
}

func TestServiceAuth_CIDRMismatchRejected(t *testing.T) {
	code, _ := callServiceAuth([]string{"secret-1"}, nil, []string{"10.0.0.0/8"}, "X-Service-Token", "secret-1", "203.0.113.9:1234")
	if code != http.StatusForbidden {
		t.Fatalf("IP 不在白名单应 403，实际 %d", code)
	}
}

func TestServiceAuth_CIDRMatchAllows(t *testing.T) {
	code, scope := callServiceAuth([]string{"secret-1"}, nil, []string{"10.0.0.0/8"}, "X-Service-Token", "secret-1", "10.1.2.3:1234")
	if code != http.StatusOK || scope != ScopeService {
		t.Fatalf("令牌正确且 IP 命中应放行，实际 code=%d scope=%q", code, scope)
	}
}

func TestServiceAuth_CIDROnlyAllows(t *testing.T) {
	// 仅配置 IP 白名单（无令牌）时，按网络维度鉴权。
	code, scope := callServiceAuth(nil, nil, []string{"10.0.0.0/8"}, "", "", "10.1.2.3:1234")
	if code != http.StatusOK || scope != ScopeService {
		t.Fatalf("仅白名单命中应放行，实际 code=%d scope=%q", code, scope)
	}
}
