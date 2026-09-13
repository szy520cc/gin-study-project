package model

import "fmt"

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

// FormatCutProgress 把切流比例格式化为百分比文案（0.5 → 50%），0 或无显示为 -。
func FormatCutProgress(cutNum float64) string {
	if cutNum <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", cutNum*100)
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
	Page     int `form:"page" binding:"omitempty,min=1,max=10000"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"`
	// ProjectID 所属项目（project 表主键的字符串形式），空串表示不过滤
	ProjectID    string `form:"project_id" binding:"omitempty,max=100"`
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
	ID           uint64 `json:"id"`
	ProjectID    string `json:"project_id"`
	ConfigPackID uint64 `json:"config_pack_id"`
	// ProjectName / ConfigPackName 是给人看的可读名称：
	// project_id 是字符串主键、config_pack_id 是数字主键，直接展示是一串编号，
	// 由 service 层批量回填（ToResponse 不查库，保持无副作用）。
	ProjectName    string `json:"project_name"`
	ProjectLogo    string `json:"project_logo"`
	ConfigPackName string `json:"config_pack_name"`
	ConfigPackLogo string `json:"config_pack_logo"`
	Name           string `json:"name"`
	Logo           string `json:"logo"`
	Type           string `json:"type"`
	Version        string `json:"version"`
	Status         uint8  `json:"status"`
	StatusText     string `json:"status_text"`
	IsLatest       uint8  `json:"is_latest"`
	IsLatestText   string `json:"is_latest_text"`
	Remark         string `json:"remark"`
	CreatedUser    string `json:"created_user"`
	UpdatedUser    string `json:"updated_user"`
	CreatedAt      int64  `json:"created_at"`
	CreatedAtText  string `json:"created_at_text"`
	UpdatedAt      int64  `json:"updated_at"`
	UpdatedAtText  string `json:"updated_at_text"`
	CutNum         float64 `json:"cut_num"`
	CutAt          int64   `json:"cut_at"`
	CutAtText      string  `json:"cut_at_text"`
	CutVersion     string  `json:"cut_version"`
	CutBy          string  `json:"cut_by"`
	CutProgress    string  `json:"cut_progress"`
	// HasActiveVersion / RuleReady 是「切流 / 发布」的前置条件标记，
	// 由 service 层批量回填（ToResponse 不查库，保持无副作用）：
	//   - HasActiveVersion：同 logo 是否存在 status=1 的线上版本（切流的硬前提，
	//     灰度状态要挂到线上版本行上）；
	//   - RuleReady：该 config 是否已保存通过校验的规则内容
	//     （无规则则发布/切流后线上 eval 会报「规则未配置」）。
	HasActiveVersion bool `json:"has_active_version"`
	RuleReady        bool `json:"rule_ready"`
}

// ImportConfigFieldsRequest 根据规则占位符导入字段默认值请求
//
// 前端「试运行」区的「导入配置」按钮把当前编辑框里的 Starlark 规则原文（含 ##id**path##）
// 发到后端，由后端解析占位符、读取字段默认值并组装成可直接运行的 JSON。
type ImportConfigFieldsRequest struct {
	ProjectID string `json:"project_id" binding:"required,max=100"`
	Rule      string `json:"rule" binding:"required"`
}

// ImportConfigFieldsResponse 导入结果：已格式化的 JSON 字符串
type ImportConfigFieldsResponse struct {
	ImportedJSON string `json:"imported_json"`
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
		CutProgress:   FormatCutProgress(c.CutNum),
	}
}
