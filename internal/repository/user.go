package repository

import (
	"context"

	"myproject/internal/model"

	"gorm.io/gorm"
)

// UserRepository 用户数据访问接口
type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	GetByID(ctx context.Context, id uint64) (*model.User, error)
	GetByUsername(ctx context.Context, username string) (*model.User, error)
	// ExistsByUsername / ExistsByEmail 用 count 判断存在性，
	// 避免注册流程为了「判断是否重复」把整行数据捞出来
	ExistsByUsername(ctx context.Context, username string) (bool, error)
	ExistsByEmail(ctx context.Context, email string) (bool, error)
	Update(ctx context.Context, user *model.User) error
	Delete(ctx context.Context, id uint64) error
	List(ctx context.Context, page, pageSize int) ([]*model.User, int64, error)
}

// userRepository 用户数据访问实现
type userRepository struct {
	base
}

// NewUser 创建用户 repository
func NewUser(db *gorm.DB) UserRepository {
	return &userRepository{base: newBase(db)}
}

// Create 创建用户
func (r *userRepository) Create(ctx context.Context, user *model.User) error {
	return wrapErr(r.conn(ctx).Create(user).Error)
}

// GetByID 根据 ID 获取用户
func (r *userRepository) GetByID(ctx context.Context, id uint64) (*model.User, error) {
	var user model.User
	if err := r.conn(ctx).First(&user, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &user, nil
}

// GetByUsername 根据用户名获取用户
func (r *userRepository) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	if err := r.conn(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &user, nil
}

// ExistsByUsername 用户名是否已存在
func (r *userRepository) ExistsByUsername(ctx context.Context, username string) (bool, error) {
	return r.exists(ctx, "username = ?", username)
}

// ExistsByEmail 邮箱是否已存在
func (r *userRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	return r.exists(ctx, "email = ?", email)
}

func (r *userRepository) exists(ctx context.Context, query string, args ...interface{}) (bool, error) {
	var count int64
	err := r.conn(ctx).Model(&model.User{}).Where(query, args...).Limit(1).Count(&count).Error
	if err != nil {
		return false, wrapErr(err)
	}
	return count > 0, nil
}

// Update 更新用户可变字段。
//
// 不用 Save：Save 是全字段覆盖，会把调用方读到的旧快照整行写回 ——
// 并发更新时后写的请求会覆盖掉前一个请求的修改（丢更新），
// 而且每次都会重写 password / username / status 这些不该由本接口触碰的列。
// 这里显式限定可更新字段，把影响面收在白名单内。
func (r *userRepository) Update(ctx context.Context, user *model.User) error {
	// 不检查 RowsAffected：MySQL 默认返回「实际变更行数」，
	// 提交了与原值相同的内容时会是 0，不能据此判定记录不存在。
	// 存在性由 service 层的先查后写保证。
	return wrapErr(r.conn(ctx).Model(&model.User{}).
		Where("id = ?", user.ID).
		Select("email", "phone", "avatar").
		Updates(user).Error)
}

// Delete 删除用户
func (r *userRepository) Delete(ctx context.Context, id uint64) error {
	return wrapErr(r.conn(ctx).Delete(&model.User{}, id).Error)
}

// List 分页获取用户列表
func (r *userRepository) List(ctx context.Context, page, pageSize int) ([]*model.User, int64, error) {
	var users []*model.User
	var total int64

	if err := r.conn(ctx).Model(&model.User{}).Count(&total).Error; err != nil {
		return nil, 0, wrapErr(err)
	}
	if total == 0 {
		return []*model.User{}, 0, nil
	}

	offset := (page - 1) * pageSize
	err := r.conn(ctx).Model(&model.User{}).
		Order("id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&users).Error
	if err != nil {
		return nil, 0, wrapErr(err)
	}

	return users, total, nil
}
