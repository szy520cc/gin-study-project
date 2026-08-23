package test

import (
	"context"
	"testing"

	"myproject/internal/model"
	"myproject/internal/repository"
	"myproject/internal/service"
	"myproject/pkg/auth"
	"myproject/pkg/errcode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockUserRepo 手写 mock：repository 已经是接口，无需引入 mock 框架就能测 service。
// 原来的测试全是 t.Skip + 空断言，等于没有测试。
type mockUserRepo struct {
	users            map[uint64]*model.User
	nextID           uint64
	existsByUsername bool
	existsByEmail    bool
	listPageSize     int // 记录 service 实际传下来的 pageSize，用于验证上限收口
	createErr        error
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: make(map[uint64]*model.User), nextID: 1}
}

func (m *mockUserRepo) Create(_ context.Context, user *model.User) error {
	if m.createErr != nil {
		return m.createErr
	}
	user.ID = m.nextID
	m.nextID++
	m.users[user.ID] = user
	return nil
}

func (m *mockUserRepo) GetByID(_ context.Context, id uint64) (*model.User, error) {
	if u, ok := m.users[id]; ok {
		return u, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockUserRepo) GetByUsername(_ context.Context, username string) (*model.User, error) {
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockUserRepo) ExistsByUsername(context.Context, string) (bool, error) {
	return m.existsByUsername, nil
}

func (m *mockUserRepo) ExistsByEmail(context.Context, string) (bool, error) {
	return m.existsByEmail, nil
}

func (m *mockUserRepo) Update(_ context.Context, user *model.User) error {
	m.users[user.ID] = user
	return nil
}

func (m *mockUserRepo) Delete(_ context.Context, id uint64) error {
	delete(m.users, id)
	return nil
}

func (m *mockUserRepo) List(_ context.Context, _, pageSize int) ([]*model.User, int64, error) {
	m.listPageSize = pageSize
	out := make([]*model.User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u)
	}
	return out, int64(len(out)), nil
}

func TestUserService_Register(t *testing.T) {
	ctx := context.Background()

	t.Run("成功注册并对密码做哈希", func(t *testing.T) {
		repo := newMockUserRepo()
		svc := service.NewUserService(repo, nil)

		resp, err := svc.Register(ctx, &model.UserRegisterRequest{
			Username: "alice",
			Password: "secret123",
			Email:    "alice@example.com",
		})

		require.NoError(t, err)
		assert.Equal(t, "alice", resp.Username)
		assert.Equal(t, model.UserStatusNormal, resp.Status)

		stored := repo.users[resp.ID]
		require.NotNil(t, stored)
		assert.NotEqual(t, "secret123", stored.Password, "密码必须哈希后存储")
		assert.True(t, auth.VerifyPassword(stored.Password, "secret123"))
	})

	t.Run("用户名重复", func(t *testing.T) {
		repo := newMockUserRepo()
		repo.existsByUsername = true
		svc := service.NewUserService(repo, nil)

		_, err := svc.Register(ctx, &model.UserRegisterRequest{
			Username: "alice", Password: "secret123", Email: "a@example.com",
		})

		assert.ErrorIs(t, err, errcode.ErrUserAlreadyExist)
	})

	t.Run("邮箱重复", func(t *testing.T) {
		repo := newMockUserRepo()
		repo.existsByEmail = true
		svc := service.NewUserService(repo, nil)

		_, err := svc.Register(ctx, &model.UserRegisterRequest{
			Username: "bob", Password: "secret123", Email: "a@example.com",
		})

		assert.ErrorIs(t, err, errcode.ErrEmailAlreadyExist)
	})

	t.Run("并发注册撞唯一索引时转成业务错误", func(t *testing.T) {
		repo := newMockUserRepo()
		repo.createErr = repository.ErrConflict
		svc := service.NewUserService(repo, nil)

		_, err := svc.Register(ctx, &model.UserRegisterRequest{
			Username: "carol", Password: "secret123", Email: "c@example.com",
		})

		assert.ErrorIs(t, err, errcode.ErrUserAlreadyExist)
	})
}

func TestUserService_Login(t *testing.T) {
	ctx := context.Background()

	newSvcWithUser := func(status int8) (*mockUserRepo, service.UserService) {
		repo := newMockUserRepo()
		hashed, err := auth.HashPassword("secret123")
		if err != nil {
			t.Fatal(err)
		}
		repo.users[1] = &model.User{
			ID: 1, Username: "alice", Password: hashed,
			Email: "alice@example.com", Status: status,
		}
		repo.nextID = 2
		return repo, service.NewUserService(repo, nil)
	}

	t.Run("密码正确", func(t *testing.T) {
		_, svc := newSvcWithUser(model.UserStatusNormal)

		user, err := svc.Login(ctx, &model.UserLoginRequest{Username: "alice", Password: "secret123"})

		require.NoError(t, err)
		assert.Equal(t, uint64(1), user.ID)
	})

	t.Run("密码错误", func(t *testing.T) {
		_, svc := newSvcWithUser(model.UserStatusNormal)

		_, err := svc.Login(ctx, &model.UserLoginRequest{Username: "alice", Password: "wrong-pass"})

		assert.ErrorIs(t, err, errcode.ErrPasswordIncorrect)
	})

	t.Run("用户不存在返回与密码错误一致的错误，避免账号枚举", func(t *testing.T) {
		_, svc := newSvcWithUser(model.UserStatusNormal)

		_, err := svc.Login(ctx, &model.UserLoginRequest{Username: "nobody", Password: "secret123"})

		assert.ErrorIs(t, err, errcode.ErrPasswordIncorrect)
	})

	t.Run("用户被禁用", func(t *testing.T) {
		_, svc := newSvcWithUser(model.UserStatusDisabled)

		_, err := svc.Login(ctx, &model.UserLoginRequest{Username: "alice", Password: "secret123"})

		assert.ErrorIs(t, err, errcode.ErrUserDisabled)
	})
}

// TestUserService_ListPageSizeCap 验证分页上限收口在 service 层，
// 防止 page_size=1000000 直接打穿数据库。
func TestUserService_ListPageSizeCap(t *testing.T) {
	repo := newMockUserRepo()
	svc := service.NewUserService(repo, nil)

	_, _, err := svc.List(context.Background(), 1, 1000000)

	require.NoError(t, err)
	assert.Equal(t, 100, repo.listPageSize, "pageSize 应被限制为 100")
}
