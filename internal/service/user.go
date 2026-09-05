// Package service 是业务逻辑层：校验业务规则、划事务边界、把数据层错误翻译成业务错误。
//
// 这一层全部是包级函数，没有 interface、没有构造函数、没有 struct 字段注入 ——
// controller 直接 service.CreateOrder(ctx, ...) 调用。
//
// SQL 不写在这里：所有数据库读写都走 internal/data 的包级函数。
// 这一层看不见 *gorm.DB，也不该出现 Where/Joins；判断错误类型用
// data.IsNotFound / data.IsDuplicate。
package service

import (
	"context"
	"errors"

	"myproject/internal/data"
	"myproject/internal/model"
	"myproject/pkg/auth"
	"myproject/pkg/errcode"
)

// ---------- 用户 ----------

// Register 用户注册
func Register(ctx context.Context, req *model.UserRegisterRequest) (*model.UserResponse, error) {
	exist, err := data.ExistsUsername(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	if exist {
		return nil, errcode.ErrUserAlreadyExist
	}

	exist, err = data.ExistsEmail(ctx, req.Email)
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
	if err := data.CreateUser(ctx, user); err != nil {
		// 上面的存在性检查与这次写入之间有竞态，唯一索引是兜底
		if data.IsDuplicate(err) {
			return nil, errcode.ErrUserAlreadyExist
		}
		return nil, err
	}

	return user.ToResponse(), nil
}

// Login 校验账号密码，成功后返回用户实体供 controller 签发 token
func Login(ctx context.Context, req *model.UserLoginRequest) (*model.User, error) {
	user, err := data.GetUserByUsername(ctx, req.Username)
	if err != nil {
		if data.IsNotFound(err) {
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

// GetUser 本人视角，返回含 email/phone 的完整信息
func GetUser(ctx context.Context, id uint64) (*model.UserResponse, error) {
	user, err := getUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return user.ToResponse(), nil
}

// GetUserPublic 他人视角，只返回公开字段
func GetUserPublic(ctx context.Context, id uint64) (*model.UserPublicResponse, error) {
	user, err := getUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return user.ToPublicResponse(), nil
}

// UpdateUser 更新用户可变字段
func UpdateUser(ctx context.Context, id uint64, req *model.UserUpdateRequest) (*model.UserResponse, error) {
	user, err := getUser(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.Email != "" && req.Email != user.Email {
		exist, err := data.ExistsEmail(ctx, req.Email)
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

	if err := data.UpdateUserProfile(ctx, id, user); err != nil {
		// 上面的存在性检查与这次写入之间有竞态，唯一索引是兜底
		if data.IsDuplicate(err) {
			return nil, errcode.ErrEmailAlreadyExist
		}
		return nil, err
	}

	return user.ToResponse(), nil
}

// ListUsers 分页获取用户列表（只含公开字段）。
// 列表对所有登录用户开放，所以不能返回 email/phone。
func ListUsers(ctx context.Context, page, pageSize int) ([]*model.UserPublicResponse, int64, error) {
	page, pageSize = model.NormalizePage(page, pageSize)

	users, total, err := data.ListUsers(ctx, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	list := make([]*model.UserPublicResponse, 0, len(users))
	for _, u := range users {
		list = append(list, u.ToPublicResponse())
	}
	return list, total, nil
}

// getUser 取用户并把「不存在」转成业务错误
func getUser(ctx context.Context, id uint64) (*model.User, error) {
	user, err := data.GetUserByID(ctx, id)
	if err != nil {
		if data.IsNotFound(err) {
			return nil, errcode.ErrUserNotFound
		}
		return nil, err
	}
	return user, nil
}
