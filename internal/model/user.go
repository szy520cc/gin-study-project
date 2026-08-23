package model

import (
	"time"
)

// User 用户模型
type User struct {
	ID        uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	Username  string    `json:"username" gorm:"type:varchar(50);uniqueIndex;not null"`
	Password  string    `json:"-" gorm:"type:varchar(255);not null"`
	Email     string    `json:"email" gorm:"type:varchar(100);uniqueIndex"`
	Phone     string    `json:"phone" gorm:"type:varchar(20)"`
	Avatar    string    `json:"avatar" gorm:"type:varchar(255)"`
	Status    int8      `json:"status" gorm:"type:tinyint;default:1;comment:1-正常 0-禁用"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// TableName 指定表名
func (User) TableName() string {
	return "users"
}

// 用户状态
const (
	UserStatusDisabled int8 = 0 // 禁用
	UserStatusNormal   int8 = 1 // 正常
)

// UserLoginRequest 登录请求
type UserLoginRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Password string `json:"password" binding:"required,min=6,max=50"`
}

// UserRegisterRequest 注册请求
type UserRegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Password string `json:"password" binding:"required,min=6,max=50"`
	Email    string `json:"email" binding:"required,email"`
	Phone    string `json:"phone" binding:"omitempty,len=11"`
}

// UserUpdateRequest 更新用户请求
type UserUpdateRequest struct {
	Email  string `json:"email" binding:"omitempty,email"`
	Phone  string `json:"phone" binding:"omitempty,len=11"`
	Avatar string `json:"avatar" binding:"omitempty,url"`
}

// UserResponse 用户响应。
// 含 email / phone / status，只用于「本人」视角（/users/profile）。
type UserResponse struct {
	ID        uint64    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone"`
	Avatar    string    `json:"avatar"`
	Status    int8      `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// UserPublicResponse 他人视角的用户信息。
//
// 单独一个结构体而不是给 UserResponse 加 omitempty：
// 列表接口和查他人接口只挂了登录校验，任何注册用户都能调 ——
// 用同一个响应体就等于把全库的 email/phone（PII）和 status（内部启禁用状态）
// 开放给所有登录用户批量导出。接了角色体系后，管理端再单独用 UserResponse。
type UserPublicResponse struct {
	ID        uint64    `json:"id"`
	Username  string    `json:"username"`
	Avatar    string    `json:"avatar"`
	CreatedAt time.Time `json:"created_at"`
}

// LoginResponse 登录响应。
// 显式声明结构体而非 gin.H，好处是 swagger 能生成文档、字段变更有编译期保障。
type LoginResponse struct {
	Token     string        `json:"token"`
	ExpiresIn int64         `json:"expires_in"` // 秒
	User      *UserResponse `json:"user"`
}

// ToResponse 将 User 转换为 UserResponse（本人视角，含 PII）
func (u *User) ToResponse() *UserResponse {
	return &UserResponse{
		ID:        u.ID,
		Username:  u.Username,
		Email:     u.Email,
		Phone:     u.Phone,
		Avatar:    u.Avatar,
		Status:    u.Status,
		CreatedAt: u.CreatedAt,
	}
}

// ToPublicResponse 将 User 转换为他人可见的公开信息
func (u *User) ToPublicResponse() *UserPublicResponse {
	return &UserPublicResponse{
		ID:        u.ID,
		Username:  u.Username,
		Avatar:    u.Avatar,
		CreatedAt: u.CreatedAt,
	}
}
