package test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"myproject/internal/config"
	"myproject/internal/middleware"
	"myproject/internal/model"
	"myproject/internal/module"
	"myproject/internal/router"
	"myproject/internal/service"
	"myproject/pkg/auth"
	"myproject/pkg/errcode"
	"myproject/pkg/health"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubUserService 只实现测试用到的方法，其余返回零值
type stubUserService struct {
	service.UserService
	loginUser *model.User
	profile   *model.UserResponse
}

func (s *stubUserService) Login(context.Context, *model.UserLoginRequest) (*model.User, error) {
	if s.loginUser == nil {
		return nil, errcode.ErrPasswordIncorrect
	}
	return s.loginUser, nil
}

func (s *stubUserService) GetByID(_ context.Context, id uint64) (*model.UserResponse, error) {
	if s.profile == nil || s.profile.ID != id {
		return nil, errcode.ErrUserNotFound
	}
	return s.profile, nil
}

type stubOrderService struct {
	service.OrderService
}

// newTestServer 组装一个真实的 gin engine（含全部中间件与真实路由表），
// 只把 service 换成 stub —— 路由声明仍然来自 internal/module，
// 不会出现「测试里抄了一份路径、线上改了路径测试还绿」的情况。
func newTestServer(t *testing.T, user service.UserService) (http.Handler, *auth.JWTManager) {
	t.Helper()

	cfg := &config.Config{}
	cfg.Server.Mode = "test"
	cfg.Server.RequestTimeout = 5
	cfg.Server.MaxBodyBytes = 1024
	cfg.CORS.AllowOrigins = []string{"*"}

	jwtManager := auth.NewJWTManager(strings.Repeat("k", 32), time.Hour, "myproject")

	engine, err := router.Setup(cfg, health.NewRegistry(time.Second, 0),
		module.Deps{JWT: jwtManager}, stubModules(user))
	require.NoError(t, err)
	return engine, jwtManager
}

// stubModules 用 stub service 注册全部业务模块，无需数据库
func stubModules(user service.UserService) []module.Register {
	return []module.Register{
		func(g *gin.RouterGroup, d module.Deps) { module.UserWith(g, d, user) },
		func(g *gin.RouterGroup, d module.Deps) { module.OrderWith(g, d, &stubOrderService{}) },
	}
}

func TestHTTP_LivezAndNotFound(t *testing.T) {
	srv, _ := newTestServer(t, &stubUserService{})

	t.Run("livez 恒定可用", func(t *testing.T) {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("未匹配路由返回 JSON 而非纯文本", func(t *testing.T) {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/not-exist", nil))

		assert.Equal(t, http.StatusNotFound, w.Code)
		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, float64(errcode.ErrNotFound.Code()), body["code"])
	})

	t.Run("响应带回 X-Request-ID", func(t *testing.T) {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/livez", nil))

		assert.NotEmpty(t, w.Header().Get(middleware.RequestIDHeader))
	})

	t.Run("透传上游的 X-Request-ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/livez", nil)
		req.Header.Set(middleware.RequestIDHeader, "trace-from-gateway")

		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, "trace-from-gateway", w.Header().Get(middleware.RequestIDHeader))
	})
}

func TestHTTP_AuthGuard(t *testing.T) {
	srv, jwtManager := newTestServer(t, &stubUserService{
		profile: &model.UserResponse{ID: 7, Username: "alice"},
	})

	t.Run("缺少 token 返回 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/users/profile", nil))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("token 格式错误返回 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/profile", nil)
		req.Header.Set("Authorization", "token abc")

		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("合法 token 可访问", func(t *testing.T) {
		token, err := jwtManager.GenerateToken(7, "alice")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/profile", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("非法路径参数返回 400", func(t *testing.T) {
		token, err := jwtManager.GenerateToken(7, "alice")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/abc", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("只能删除自己的账号", func(t *testing.T) {
		token, err := jwtManager.GenerateToken(7, "alice")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/8", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestHTTP_LoginReturnsToken(t *testing.T) {
	srv, _ := newTestServer(t, &stubUserService{
		loginUser: &model.User{ID: 1, Username: "alice", Status: model.UserStatusNormal},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/login",
		strings.NewReader(`{"username":"alice","password":"secret123"}`))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Code int `json:"code"`
		Data struct {
			Token     string `json:"token"`
			ExpiresIn int64  `json:"expires_in"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 0, body.Code)
	assert.NotEmpty(t, body.Data.Token)
	assert.Equal(t, int64(3600), body.Data.ExpiresIn)
}
