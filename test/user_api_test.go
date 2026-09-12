package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"myproject/internal/model"
	"myproject/pkg/errcode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- 请求辅助 ----------

type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Details string          `json:"details"`
	Data    json.RawMessage `json:"data"`
}

// do 发一个请求，返回状态码与解析后的响应体
func do(t *testing.T, method, path, token string, body interface{}) (int, apiResp) {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	w := httptest.NewRecorder()
	testEngine.ServeHTTP(w, req)

	var resp apiResp
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
	}
	return w.Code, resp
}

// newUser 注册并登录一个随机用户，返回 id 与 token；用例结束时自动清理
func newUser(t *testing.T) (uint64, string) {
	t.Helper()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e12)
	username := "it_" + suffix
	req := model.UserRegisterRequest{
		Username: username,
		Password: "secret123",
		Email:    username + "@example.com",
		Phone:    "138" + suffix[:8],
	}

	code, resp := do(t, http.MethodPost, "/api/v1/users/register", "", req)
	require.Equal(t, http.StatusOK, code, "注册失败: %s %s", resp.Message, resp.Details)

	var created model.UserResponse
	require.NoError(t, json.Unmarshal(resp.Data, &created))

	code, resp = do(t, http.MethodPost, "/api/v1/users/login", "",
		model.UserLoginRequest{Username: username, Password: "secret123"})
	require.Equal(t, http.StatusOK, code)

	var login model.LoginResponse
	require.NoError(t, json.Unmarshal(resp.Data, &login))
	require.NotEmpty(t, login.Token)

	t.Cleanup(func() {
		testDB.Delete(&model.User{}, created.ID)
	})

	return created.ID, login.Token
}

// ---------- 用户 ----------

// TestUserFlow 注册 → 登录 → 查自己 → 改资料 → 列表 → 查他人 → 删除
func TestUserFlow(t *testing.T) {
	requireDB(t)
	id, token := newUser(t)

	t.Run("查自己带 email", func(t *testing.T) {
		code, resp := do(t, http.MethodGet, "/api/v1/users/profile", token, nil)
		require.Equal(t, http.StatusOK, code)
		var me model.UserResponse
		require.NoError(t, json.Unmarshal(resp.Data, &me))
		assert.Equal(t, id, me.ID)
		assert.NotEmpty(t, me.Email, "本人视角应含 email")
	})

	t.Run("改资料落库", func(t *testing.T) {
		code, resp := do(t, http.MethodPut, "/api/v1/users/profile", token,
			model.UserUpdateRequest{Avatar: "https://example.com/a.png"})
		require.Equal(t, http.StatusOK, code)

		var updated model.UserResponse
		require.NoError(t, json.Unmarshal(resp.Data, &updated))
		assert.Equal(t, "https://example.com/a.png", updated.Avatar)

		var row model.User
		require.NoError(t, testDB.First(&row, id).Error)
		assert.Equal(t, "https://example.com/a.png", row.Avatar, "数据库里也要是新值")
	})

	t.Run("查他人不返回 PII", func(t *testing.T) {
		code, resp := do(t, http.MethodGet, fmt.Sprintf("/api/v1/users/%d", id), token, nil)
		require.Equal(t, http.StatusOK, code)
		assert.NotContains(t, string(resp.Data), "email", "他人视角不应含 email")
		assert.NotContains(t, string(resp.Data), "phone")
	})

	t.Run("列表分页字段齐全", func(t *testing.T) {
		code, resp := do(t, http.MethodGet, "/api/v1/users?page=1&page_size=2", token, nil)
		require.Equal(t, http.StatusOK, code)
		var list struct {
			List     []model.UserPublicResponse `json:"list"`
			Total    int64                      `json:"total"`
			Page     int                        `json:"page"`
			PageSize int                        `json:"page_size"`
		}
		require.NoError(t, json.Unmarshal(resp.Data, &list))
		assert.Equal(t, 1, list.Page)
		assert.Equal(t, 2, list.PageSize)
		assert.LessOrEqual(t, len(list.List), 2)
		assert.GreaterOrEqual(t, list.Total, int64(1))
	})
}

// TestUserRegisterConflict 重复用户名/邮箱要返回业务错误而不是 500
func TestUserRegisterConflict(t *testing.T) {
	requireDB(t)
	_, token := newUser(t)

	code, resp := do(t, http.MethodGet, "/api/v1/users/profile", token, nil)
	require.Equal(t, http.StatusOK, code)
	var me model.UserResponse
	require.NoError(t, json.Unmarshal(resp.Data, &me))

	code, resp = do(t, http.MethodPost, "/api/v1/users/register", "", model.UserRegisterRequest{
		Username: me.Username,
		Password: "secret123",
		Email:    "other_" + me.Email,
	})
	// 用户已存在归 400（errcode.ErrUserAlreadyExist），不是 409
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, errcode.ErrUserAlreadyExist.Code(), resp.Code)
}

// TestLoginWrongPassword 密码错与用户不存在必须是同一个错误，避免账号枚举
func TestLoginWrongPassword(t *testing.T) {
	requireDB(t)
	_, token := newUser(t)

	_, resp := do(t, http.MethodGet, "/api/v1/users/profile", token, nil)
	var me model.UserResponse
	require.NoError(t, json.Unmarshal(resp.Data, &me))

	code1, r1 := do(t, http.MethodPost, "/api/v1/users/login", "",
		model.UserLoginRequest{Username: me.Username, Password: "wrong-password"})
	code2, r2 := do(t, http.MethodPost, "/api/v1/users/login", "",
		model.UserLoginRequest{Username: "no_such_user_zzz", Password: "wrong-password"})

	assert.Equal(t, code1, code2)
	assert.Equal(t, r1.Code, r2.Code)
	assert.Equal(t, errcode.ErrPasswordIncorrect.Code(), r1.Code)
}
