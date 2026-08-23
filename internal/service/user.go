package service

import (
	"context"
	"errors"

	"myproject/internal/model"
	"myproject/internal/repository"
	"myproject/pkg/auth"
	"myproject/pkg/errcode"
	"myproject/pkg/transaction"
)

// UserService 用户业务逻辑接口
type UserService interface {
	Register(ctx context.Context, req *model.UserRegisterRequest) (*model.UserResponse, error)
	Login(ctx context.Context, req *model.UserLoginRequest) (*model.User, error)
	// GetByID 本人视角，返回含 email/phone 的完整信息
	GetByID(ctx context.Context, id uint64) (*model.UserResponse, error)
	// GetPublicByID 他人视角，只返回公开字段
	GetPublicByID(ctx context.Context, id uint64) (*model.UserPublicResponse, error)
	Update(ctx context.Context, id uint64, req *model.UserUpdateRequest) (*model.UserResponse, error)
	Delete(ctx context.Context, id uint64) error
	// List 列表只返回公开字段：这个接口对所有登录用户开放
	List(ctx context.Context, page, pageSize int) ([]*model.UserPublicResponse, int64, error)
}

// userService 用户业务逻辑实现
type userService struct {
	userRepo repository.UserRepository
	tx       transaction.Manager
}

// NewUserService 创建用户业务逻辑实例
func NewUserService(userRepo repository.UserRepository, tx transaction.Manager) UserService {
	return &userService{userRepo: userRepo, tx: tx}
}

// Register 用户注册
func (s *userService) Register(ctx context.Context, req *model.UserRegisterRequest) (*model.UserResponse, error) {
	// 用 count 判断重复，不再把整行捞出来
	exist, err := s.userRepo.ExistsByUsername(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	if exist {
		return nil, errcode.ErrUserAlreadyExist
	}

	exist, err = s.userRepo.ExistsByEmail(ctx, req.Email)
	if err != nil {
		return nil, err
	}
	if exist {
		return nil, errcode.ErrEmailAlreadyExist
	}

	hashed, err := auth.HashPassword(req.Password)
	if err != nil {
		// 超长是输入问题（validator 的 max 按字符数算，中文密码很容易超 72 字节），
		// 必须返回 400；其余才是内部故障
		if errors.Is(err, auth.ErrPasswordTooLong) {
			return nil, errcode.ErrInvalidParams.WithDetails("密码不能超过 %d 字节（一个中文字符占 3 字节）", auth.MaxPasswordBytes)
		}
		return nil, errcode.ErrInternal.WithCause(err)
	}

	user := &model.User{
		Username: req.Username,
		Password: hashed,
		Email:    req.Email,
		Phone:    req.Phone,
		Status:   model.UserStatusNormal,
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		// 并发注册时唯一索引兜底：DB 层冲突转成业务错误
		if errors.Is(err, repository.ErrConflict) {
			return nil, errcode.ErrUserAlreadyExist
		}
		return nil, err
	}

	return user.ToResponse(), nil
}

// Login 用户登录
func (s *userService) Login(ctx context.Context, req *model.UserLoginRequest) (*model.User, error) {
	user, err := s.userRepo.GetByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// 用户不存在与密码错误返回同一个错误，避免账号枚举
			return nil, errcode.ErrPasswordIncorrect
		}
		return nil, err
	}

	if !auth.VerifyPassword(user.Password, req.Password) {
		return nil, errcode.ErrPasswordIncorrect
	}

	if user.Status != model.UserStatusNormal {
		return nil, errcode.ErrUserDisabled
	}

	return user, nil
}

// GetByID 根据 ID 获取用户（本人视角）
func (s *userService) GetByID(ctx context.Context, id uint64) (*model.UserResponse, error) {
	user, err := s.getUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return user.ToResponse(), nil
}

// GetPublicByID 根据 ID 获取用户的公开信息（他人视角）
func (s *userService) GetPublicByID(ctx context.Context, id uint64) (*model.UserPublicResponse, error) {
	user, err := s.getUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return user.ToPublicResponse(), nil
}

// Update 更新用户信息
func (s *userService) Update(ctx context.Context, id uint64, req *model.UserUpdateRequest) (*model.UserResponse, error) {
	user, err := s.getUser(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.Email != "" && req.Email != user.Email {
		exist, err := s.userRepo.ExistsByEmail(ctx, req.Email)
		if err != nil {
			return nil, err
		}
		if exist {
			return nil, errcode.ErrEmailAlreadyExist
		}
		user.Email = req.Email
	}
	if req.Phone != "" {
		user.Phone = req.Phone
	}
	if req.Avatar != "" {
		user.Avatar = req.Avatar
	}

	if err := s.userRepo.Update(ctx, user); err != nil {
		// 与 Register 一致：唯一索引冲突是业务错误，不是内部故障。
		// ExistsByEmail 与 Update 之间存在竞态，这里是兜底。
		if errors.Is(err, repository.ErrConflict) {
			return nil, errcode.ErrEmailAlreadyExist
		}
		return nil, err
	}

	return user.ToResponse(), nil
}

// Delete 删除用户
func (s *userService) Delete(ctx context.Context, id uint64) error {
	if _, err := s.getUser(ctx, id); err != nil {
		return err
	}
	return s.userRepo.Delete(ctx, id)
}

// List 分页获取用户列表（只含公开字段）
func (s *userService) List(ctx context.Context, page, pageSize int) ([]*model.UserPublicResponse, int64, error) {
	page, pageSize = normalizePage(page, pageSize)

	users, total, err := s.userRepo.List(ctx, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	responses := make([]*model.UserPublicResponse, 0, len(users))
	for _, user := range users {
		responses = append(responses, user.ToPublicResponse())
	}

	return responses, total, nil
}

// getUser 取用户并把「不存在」转成业务错误，收敛重复代码
func (s *userService) getUser(ctx context.Context, id uint64) (*model.User, error) {
	user, err := s.userRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, errcode.ErrUserNotFound
		}
		return nil, err
	}
	return user, nil
}
