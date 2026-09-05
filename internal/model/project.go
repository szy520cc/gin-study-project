package model

import (
	"time"
)

// Project 项目模型，对应 DDL：
//
//	CREATE TABLE `project` (
//	  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
//	  `name` varchar(200) NOT NULL COMMENT '项目名称',
//	  `logo` varchar(100) NOT NULL COMMENT '项目标识',
//	  `status` tinyint unsigned NOT NULL DEFAULT '0' COMMENT '状态 1 生效， 2 废弃',
//	  `created_user` varchar(50) NOT NULL COMMENT '创建人',
//	  `updated_user` varchar(50) NOT NULL COMMENT '最后编辑人',
//	  `created_at` int NOT NULL DEFAULT '0' COMMENT '创建时间',
//	  `updated_at` int NOT NULL DEFAULT '0' COMMENT '更新时间',
//	  UNIQUE KEY `idx_logo` (`logo`)
//	)
//
// 时间戳按业务要求用 int 存 Unix 秒，不启用 gorm 的 autoCreateTime/autoUpdateTime，
// 由 service 写入 time.Now().Unix()。
type Project struct {
	ID          uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	Name        string `json:"name" gorm:"type:varchar(200);not null"`
	Logo        string `json:"logo" gorm:"type:varchar(100);uniqueIndex;not null"`
	Status      uint8  `json:"status" gorm:"type:tinyint unsigned;not null;default:0;comment:1-生效 2-废弃"`
	CreatedUser string `json:"created_user" gorm:"type:varchar(50);not null"`
	UpdatedUser string `json:"updated_user" gorm:"type:varchar(50);not null"`
	CreatedAt   int64  `json:"created_at" gorm:"type:int;not null;default:0"`
	UpdatedAt   int64  `json:"updated_at" gorm:"type:int;not null;default:0"`
}

// TableName 表名（DDL 表名是单数 project）
func (Project) TableName() string {
	return "project"
}

// 项目状态
const (
	ProjectStatusActive  uint8 = 1 // 生效
	ProjectStatusRetired uint8 = 2 // 废弃
)

// CreateProjectRequest 创建项目请求
type CreateProjectRequest struct {
	Name   string `json:"name" binding:"required,max=200"`
	Logo   string `json:"logo" binding:"required,max=100"`
	Status uint8  `json:"status" binding:"omitempty,oneof=1 2"`
}

// UpdateProjectRequest 更新项目请求。
// 语义是整行编辑（前端表单会回填全部字段），ID + 三个字段都必填。
type UpdateProjectRequest struct {
	ID     uint64 `json:"id" binding:"required"`
	Name   string `json:"name" binding:"required,max=200"`
	Logo   string `json:"logo" binding:"required,max=100"`
	Status uint8  `json:"status" binding:"required,oneof=1 2"`
}

// DeleteProjectRequest 删除项目请求
type DeleteProjectRequest struct {
	ID uint64 `json:"id" binding:"required"`
}

// ProjectQueryRequest 按主键查询单个项目请求（query 传 id）
type ProjectQueryRequest struct {
	ID uint64 `form:"id" binding:"required"`
}

// ProjectListRequest 项目列表请求
type ProjectListRequest struct {
	Page     int    `form:"page" binding:"omitempty,min=1,max=10000"`
	PageSize int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	Name     string `form:"name" binding:"omitempty,max=200"`
	// Status 用指针区分「不筛选」与「筛选生效(1)」
	Status *uint8 `form:"status" binding:"omitempty,oneof=1 2"`
}

// ProjectResponse 项目响应
type ProjectResponse struct {
	ID            uint64 `json:"id"`
	Name          string `json:"name"`
	Logo          string `json:"logo"`
	Status        uint8  `json:"status"`
	StatusText    string `json:"status_text"`
	CreatedUser   string `json:"created_user"`
	UpdatedUser   string `json:"updated_user"`
	CreatedAt     int64  `json:"created_at"`
	CreatedAtText string `json:"created_at_text"`
	UpdatedAt     int64  `json:"updated_at"`
	UpdatedAtText string `json:"updated_at_text"`
}

// ToResponse 将 Project 转换为响应体。
// created_at 是 Unix 秒，另给 *_text 方便前端直接展示。
func (p *Project) ToResponse() *ProjectResponse {
	return &ProjectResponse{
		ID:            p.ID,
		Name:          p.Name,
		Logo:          p.Logo,
		Status:        p.Status,
		StatusText:    ProjectStatusText(p.Status),
		CreatedUser:   p.CreatedUser,
		UpdatedUser:   p.UpdatedUser,
		CreatedAt:     p.CreatedAt,
		CreatedAtText: formatUnix(p.CreatedAt),
		UpdatedAt:     p.UpdatedAt,
		UpdatedAtText: formatUnix(p.UpdatedAt),
	}
}

// ProjectStatusText 状态文案
func ProjectStatusText(status uint8) string {
	switch status {
	case ProjectStatusActive:
		return "生效"
	case ProjectStatusRetired:
		return "废弃"
	default:
		return "未知"
	}
}

// formatUnix 把 Unix 秒格式化为可读时间，0 显示为空（兼容默认值）
func formatUnix(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}
