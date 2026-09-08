package model

// Config 配置模型，对应 DDL：
//
//	CREATE TABLE `config` (
//	  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
//	  `project_id` varchar(100) NOT NULL,
//	  `config_pack_id` bigint unsigned NOT NULL,
//	  `name` varchar(200) NOT NULL,
//	  `logo` varchar(200) NOT NULL,
//	  `type` varchar(50) NOT NULL,
//	  `version` varchar(50) NOT NULL,
//	  `status` tinyint unsigned NOT NULL DEFAULT '0', -- 待审核0 生效1 下线2
//	  `is_latest` tinyint unsigned NOT NULL DEFAULT '1', -- 是1 否2
//	  `remark` text,
//	  created_user/updated_user varchar(50),
//	  created_at/updated_at int unsigned,
//	  `cut_num` double NOT NULL DEFAULT '0',
//	  `cut_at` int unsigned NOT NULL DEFAULT '0',
//	  `cut_version` varchar(50) NOT NULL DEFAULT '',
//	  UNIQUE KEY `idx_logo_version` (`logo`,`version`),
//	  KEY `idx_config_pack_id` (`config_pack_id`)
//	)
//
// 身份字段（project_id/config_pack_id/logo/version）创建后不可改；
// 业务上 logo 即配置标识，logo+version 决定「哪个配置的哪个版本」。
type Config struct {
	ID           uint64  `json:"id" gorm:"primaryKey;autoIncrement"`
	ProjectID    string  `json:"project_id" gorm:"type:varchar(100);not null"`
	ConfigPackID uint64  `json:"config_pack_id" gorm:"index;not null"`
	Name         string  `json:"name" gorm:"type:varchar(200);not null"`
	Logo         string  `json:"logo" gorm:"type:varchar(200);not null;uniqueIndex:idx_logo_version"`
	Type         string  `json:"type" gorm:"type:varchar(50);not null"`
	Version      string  `json:"version" gorm:"type:varchar(50);not null;uniqueIndex:idx_logo_version"`
	Status       uint8   `json:"status" gorm:"type:tinyint unsigned;not null;default:0;comment:0-待审核 1-生效 2-下线"`
	IsLatest     uint8   `json:"is_latest" gorm:"type:tinyint unsigned;not null;default:1;comment:1-是 2-否"`
	Remark       string  `json:"remark" gorm:"type:text"`
	CreatedUser  string  `json:"created_user" gorm:"type:varchar(50);not null"`
	UpdatedUser  string  `json:"updated_user" gorm:"type:varchar(50);not null"`
	CreatedAt    int64   `json:"created_at" gorm:"type:int unsigned;not null;default:0"`
	UpdatedAt    int64   `json:"updated_at" gorm:"type:int unsigned;not null;default:0"`
	CutNum       float64 `json:"cut_num" gorm:"type:double;not null;default:0"`
	CutAt        int64   `json:"cut_at" gorm:"type:int unsigned;not null;default:0"`
	CutVersion   string  `json:"cut_version" gorm:"type:varchar(50);not null;default:''"`
	CutBy        string  `json:"cut_by" gorm:"type:varchar(50);not null;default:''"`
}

// TableName 表名（DDL 表名 config）
func (Config) TableName() string { return "config" }

// 配置状态：与配置包一致（0 待审核是合法业务状态）
const (
	ConfigStatusPending uint8 = 0
	ConfigStatusActive  uint8 = 1
	ConfigStatusOffline uint8 = 2
)

// 是否最新版本
const (
	ConfigLatestYes uint8 = 1
	ConfigLatestNo  uint8 = 2
)

// ConfigStatusText 状态文案
func ConfigStatusText(status uint8) string { return ConfigPackStatusText(status) }

// ConfigLatestText 是否最新文案
func ConfigLatestText(isLatest uint8) string {
	if isLatest == ConfigLatestYes {
		return "是"
	}
	return "否"
}

// CreateConfigRequest 创建配置请求
type CreateConfigRequest struct {
	ProjectID    string  `json:"project_id" binding:"required,max=100"`
	ConfigPackID uint64  `json:"config_pack_id" binding:"required"`
	Name         string  `json:"name" binding:"required,max=200"`
	Logo         string  `json:"logo" binding:"required,max=200"`
	Type         string  `json:"type" binding:"required,max=50"`
	Version      string  `json:"version" binding:"required,max=50"`
	Status       uint8   `json:"status" binding:"omitempty,oneof=0 1 2"`
	IsLatest     uint8   `json:"is_latest" binding:"omitempty,oneof=1 2"`
	Remark       string  `json:"remark" binding:"max=2000"`
	CutNum       float64 `json:"cut_num" binding:"omitempty,min=0"`
	CutAt        int64   `json:"cut_at" binding:"omitempty,min=0"`
	CutVersion   string  `json:"cut_version" binding:"max=50"`
}

// UpdateConfigRequest 更新配置请求。
// 不含 project_id/config_pack_id/logo/version（身份字段创建后不可改）。
type UpdateConfigRequest struct {
	ID         uint64  `json:"id" binding:"required"`
	Name       string  `json:"name" binding:"required,max=200"`
	Type       string  `json:"type" binding:"required,max=50"`
	Status     uint8   `json:"status" binding:"omitempty,oneof=0 1 2"`
	IsLatest   uint8   `json:"is_latest" binding:"omitempty,oneof=1 2"`
	Remark     string  `json:"remark" binding:"max=2000"`
	CutNum     float64 `json:"cut_num" binding:"omitempty,min=0"`
	CutAt      int64   `json:"cut_at" binding:"omitempty,min=0"`
	CutVersion string  `json:"cut_version" binding:"max=50"`
}

// DeleteConfigRequest 删除配置请求
type DeleteConfigRequest struct {
	ID uint64 `json:"id" binding:"required"`
}

// ConfigListRequest 配置列表请求
type ConfigListRequest struct {
	Page         int    `form:"page" binding:"omitempty,min=1,max=10000"`
	PageSize     int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	ConfigPackID uint64 `form:"config_pack_id" binding:"omitempty,min=1"`
	Name         string `form:"name" binding:"omitempty,max=200"`
	Type         string `form:"type" binding:"omitempty,max=50"`
	Status       *uint8 `form:"status" binding:"omitempty,oneof=0 1 2"`
	IsLatest     *uint8 `form:"is_latest" binding:"omitempty"`
}

// ConfigQueryRequest 按主键查询（query 传 id）
type ConfigQueryRequest struct {
	ID uint64 `form:"id" binding:"required"`
}

// ConfigResponse 配置响应
type ConfigResponse struct {
	ID           uint64  `json:"id"`
	ProjectID    string  `json:"project_id"`
	ConfigPackID uint64  `json:"config_pack_id"`
	Name         string  `json:"name"`
	Logo         string  `json:"logo"`
	Type         string  `json:"type"`
	Version      string  `json:"version"`
	Status       uint8   `json:"status"`
	StatusText   string  `json:"status_text"`
	IsLatest     uint8   `json:"is_latest"`
	IsLatestText string  `json:"is_latest_text"`
	Remark       string  `json:"remark"`
	CreatedUser  string  `json:"created_user"`
	UpdatedUser  string  `json:"updated_user"`
	CreatedAt    int64   `json:"created_at"`
	CreatedAtText string `json:"created_at_text"`
	UpdatedAt    int64   `json:"updated_at"`
	UpdatedAtText string `json:"updated_at_text"`
	CutNum       float64 `json:"cut_num"`
	CutAt        int64   `json:"cut_at"`
	CutAtText    string  `json:"cut_at_text"`
	CutVersion   string  `json:"cut_version"`
	CutBy        string  `json:"cut_by"`
}

// ToResponse 转响应
func (c *Config) ToResponse() *ConfigResponse {
	return &ConfigResponse{
		ID:            c.ID,
		ProjectID:     c.ProjectID,
		ConfigPackID:  c.ConfigPackID,
		Name:          c.Name,
		Logo:          c.Logo,
		Type:          c.Type,
		Version:       c.Version,
		Status:        c.Status,
		StatusText:    ConfigStatusText(c.Status),
		IsLatest:      c.IsLatest,
		IsLatestText:  ConfigLatestText(c.IsLatest),
		Remark:        c.Remark,
		CreatedUser:   c.CreatedUser,
		UpdatedUser:   c.UpdatedUser,
		CreatedAt:     c.CreatedAt,
		CreatedAtText: formatUnix(c.CreatedAt),
		UpdatedAt:     c.UpdatedAt,
		UpdatedAtText: formatUnix(c.UpdatedAt),
		CutNum:        c.CutNum,
		CutAt:         c.CutAt,
		CutAtText:     formatUnix(c.CutAt),
		CutVersion:    c.CutVersion,
		CutBy:         c.CutBy,
	}
}
