package model

// ConfigPack 配置包模型，对应 DDL：
//
//	CREATE TABLE `config_pack` (
//	  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
//	  `project_id` varchar(100) NOT NULL COMMENT '项目ID 同 project 表主键',
//	  `name` varchar(200) NOT NULL COMMENT '配置包名称',
//	  `logo` varchar(200) NOT NULL COMMENT '配置包标识',
//	  `status` tinyint unsigned NOT NULL DEFAULT '0' COMMENT '待审核 0，生效 1，下线 2',
//	  `remark` text,
//	  `created_user` varchar(50) NOT NULL,
//	  `updated_user` varchar(50) NOT NULL,
//	  `created_at` int unsigned NOT NULL DEFAULT '0',
//	  `updated_at` int unsigned NOT NULL DEFAULT '0',
//	  UNIQUE KEY `idx_logo_project_id` (`logo`,`project_id`)
//	)
//
// 业务约束：logo（配置包标识）创建后不允许修改，update 请求体不提供 logo 字段。
type ConfigPack struct {
	ID          uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	ProjectID   string `json:"project_id" gorm:"type:varchar(100);not null;uniqueIndex:idx_logo_project_id"`
	Name        string `json:"name" gorm:"type:varchar(200);not null"`
	Logo        string `json:"logo" gorm:"type:varchar(200);not null;uniqueIndex:idx_logo_project_id"`
	Status      uint8  `json:"status" gorm:"type:tinyint unsigned;not null;default:0;comment:0-待审核 1-生效 2-下线"`
	Remark      string `json:"remark" gorm:"type:text"`
	CreatedUser string `json:"created_user" gorm:"type:varchar(50);not null"`
	UpdatedUser string `json:"updated_user" gorm:"type:varchar(50);not null"`
	CreatedAt   int64  `json:"created_at" gorm:"type:int unsigned;not null;default:0"`
	UpdatedAt   int64  `json:"updated_at" gorm:"type:int unsigned;not null;default:0"`
}

// TableName 表名（DDL 表名 config_pack）
func (ConfigPack) TableName() string { return "config_pack" }

// 配置包状态（0=待审核 是合法业务状态，不是空值）
const (
	ConfigPackStatusPending uint8 = 0 // 待审核
	ConfigPackStatusActive  uint8 = 1 // 生效
	ConfigPackStatusOffline uint8 = 2 // 下线
)

// ConfigPackStatusText 状态文案
func ConfigPackStatusText(status uint8) string {
	switch status {
	case ConfigPackStatusPending:
		return "待审核"
	case ConfigPackStatusActive:
		return "生效"
	case ConfigPackStatusOffline:
		return "下线"
	default:
		return "未知"
	}
}

// CreateConfigPackRequest 创建配置包请求
type CreateConfigPackRequest struct {
	ProjectID string `json:"project_id" binding:"required,max=100"`
	Name      string `json:"name" binding:"required,max=200"`
	Logo      string `json:"logo" binding:"required,max=200"`
	Status    uint8  `json:"status" binding:"omitempty,oneof=0 1 2"`
	Remark    string `json:"remark" binding:"max=2000"`
}

// UpdateConfigPackRequest 更新配置包请求。
// 不提供 logo：配置包标识创建后不允许修改。
type UpdateConfigPackRequest struct {
	ID     uint64 `json:"id" binding:"required"`
	Name   string `json:"name" binding:"required,max=200"`
	Status uint8  `json:"status" binding:"omitempty,oneof=0 1 2"`
	Remark string `json:"remark" binding:"max=2000"`
}

// DeleteConfigPackRequest 删除配置包请求
type DeleteConfigPackRequest struct {
	ID uint64 `json:"id" binding:"required"`
}

// ConfigPackListRequest 配置包列表请求
type ConfigPackListRequest struct {
	Page      int    `form:"page" binding:"omitempty,min=1,max=10000"`
	PageSize  int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	ProjectID string `form:"project_id" binding:"omitempty,max=100"`
	Name      string `form:"name" binding:"omitempty,max=200"`
	Status    *uint8 `form:"status" binding:"omitempty,oneof=0 1 2"`
}

// ConfigPackQueryRequest 按主键查询（query 传 id）
type ConfigPackQueryRequest struct {
	ID uint64 `form:"id" binding:"required"`
}

// ConfigPackResponse 配置包响应
type ConfigPackResponse struct {
	ID            uint64 `json:"id"`
	ProjectID     string `json:"project_id"`
	Name          string `json:"name"`
	Logo          string `json:"logo"`
	Status        uint8  `json:"status"`
	StatusText    string `json:"status_text"`
	Remark        string `json:"remark"`
	CreatedUser   string `json:"created_user"`
	UpdatedUser   string `json:"updated_user"`
	CreatedAt     int64  `json:"created_at"`
	CreatedAtText string `json:"created_at_text"`
	UpdatedAt     int64  `json:"updated_at"`
	UpdatedAtText string `json:"updated_at_text"`
}

// ToResponse 转响应
func (c *ConfigPack) ToResponse() *ConfigPackResponse {
	return &ConfigPackResponse{
		ID:            c.ID,
		ProjectID:     c.ProjectID,
		Name:          c.Name,
		Logo:          c.Logo,
		Status:        c.Status,
		StatusText:    ConfigPackStatusText(c.Status),
		Remark:        c.Remark,
		CreatedUser:   c.CreatedUser,
		UpdatedUser:   c.UpdatedUser,
		CreatedAt:     c.CreatedAt,
		CreatedAtText: formatUnix(c.CreatedAt),
		UpdatedAt:     c.UpdatedAt,
		UpdatedAtText: formatUnix(c.UpdatedAt),
	}
}
