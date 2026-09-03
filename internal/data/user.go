package data

import (
	"context"

	"myproject/internal/model"
)

// CreateUser 插入一条用户
func CreateUser(ctx context.Context, user *model.User) error {
	return connDb(ctx).Create(user).Error
}

// GetUserByID 按主键取用户，不存在时返回 gorm.ErrRecordNotFound
func GetUserByID(ctx context.Context, id uint64) (*model.User, error) {
	var user model.User
	if err := connDb(ctx).First(&user, id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserByUsername 按用户名取用户，不存在时返回 gorm.ErrRecordNotFound
func GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	if err := connDb(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

// ExistsUsername 用户名是否已被占用
func ExistsUsername(ctx context.Context, username string) (bool, error) {
	return existsUser(ctx, "username = ?", username)
}

// ExistsEmail 邮箱是否已被占用
func ExistsEmail(ctx context.Context, email string) (bool, error) {
	return existsUser(ctx, "email = ?", email)
}

// existsUser 用 count 判断存在性，不把整行数据捞出来
func existsUser(ctx context.Context, query string, args ...any) (bool, error) {
	var count int64
	err := connDb(ctx).Model(&model.User{}).Where(query, args...).Limit(1).Count(&count).Error
	return count > 0, err
}

// UpdateUserProfile 更新用户资料字段。
//
// 只写 email/phone/avatar 三列，用 Select 白名单 + Updates 而不是 Save：
// Save 是全字段覆盖，会把调用方读到的旧快照整行写回（并发下丢更新），
// 还会重写 password/username/status 这些不该由资料接口触碰的列。
//
// 调用方不要用返回的 RowsAffected 判断记录是否存在：MySQL 默认返回
// 「实际变更行数」，提交与原值相同的内容时是 0。
func UpdateUserProfile(ctx context.Context, id uint64, user *model.User) error {
	return connDb(ctx).Model(&model.User{}).Where("id = ?", id).
		Select("email", "phone", "avatar").Updates(user).Error
}

// ListUsers 分页查询用户，返回当页数据与总数。
// page/pageSize 由 service 归一化后传入，本层不做上限判断。
func ListUsers(ctx context.Context, page, pageSize int) ([]*model.User, int64, error) {
	var total int64
	if err := connDb(ctx).Model(&model.User{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	var users []*model.User
	err := connDb(ctx).Model(&model.User{}).Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}
