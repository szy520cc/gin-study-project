package model

type Field struct {
	ID uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	// ProjectID 所属项目（project 表主键，按 DDL 存 varchar）
	ProjectID string `json:"project_id" gorm:"type:varchar(100);not null;uniqueIndex:idx_parse_path_project_id"`
	Name      string `json:"name" gorm:"type:varchar(200);not null"`
	Type      string `json:"type" gorm:"type:varchar(100);not null"`
	// DefaultValue 默认值配置 JSON 串
	DefaultValue string `json:"default_value" gorm:"type:varchar(200);not null"`
	// ParsePath 解析路径，同一项目内唯一
	ParsePath   string `json:"parse_path" gorm:"type:varchar(500);not null;uniqueIndex:idx_parse_path_project_id"`
	Status      uint8  `json:"status" gorm:"type:tinyint unsigned;not null;default:0;comment:1-生效 2-废弃"`
	Remark      string `json:"remark" gorm:"type:text;not null"`
	CreatedUser string `json:"created_user" gorm:"type:varchar(50);not null"`
	UpdatedUser string `json:"updated_user" gorm:"type:varchar(50);not null"`
	CreatedAt   int64  `json:"created_at" gorm:"type:int;not null;default:0"`
	UpdatedAt   int64  `json:"updated_at" gorm:"type:int;not null;default:0"`
}

// TableName 表名（DDL 表名单数）
func (Field) TableName() string { return "field" }

// 字段状态
const (
	FieldStatusActive  uint8 = 1 // 生效
	FieldStatusRetired uint8 = 2 // 废弃
)

// 字段类型 code（default_value.type 与 type 列共用同一套）
const (
	FieldTypeInt    = "int"
	FieldTypeFloat  = "float"
	FieldTypeString = "string"
	FieldTypeBool   = "bool"
	FieldTypeArray  = "array"
	FieldTypeObject = "object"
)

// FieldTypeLabel 类型中文名，用于前端展示
func FieldTypeLabel(t string) string {
	switch t {
	case FieldTypeInt:
		return "整数"
	case FieldTypeFloat:
		return "浮点数"
	case FieldTypeString:
		return "字符串"
	case FieldTypeBool:
		return "布尔值"
	case FieldTypeArray:
		return "数组"
	case FieldTypeObject:
		return "对象"
	default:
		return "未知"
	}
}

// IsValidFieldType 判断类型 code 是否在白名单内
func IsValidFieldType(t string) bool {
	switch t {
	case FieldTypeInt, FieldTypeFloat, FieldTypeString, FieldTypeBool, FieldTypeArray, FieldTypeObject:
		return true
	}
	return false
}

// CreateFieldRequest 创建字段请求
type CreateFieldRequest struct {
	ProjectID    string `json:"project_id" binding:"required,max=100"`
	Name         string `json:"name" binding:"required,max=200"`
	Type         string `json:"type" binding:"required,oneof=int float string bool array object"`
	DefaultValue string `json:"default_value" binding:"required"`
	ParsePath    string `json:"parse_path" binding:"required,max=500"`
	Status       uint8  `json:"status" binding:"omitempty,oneof=1 2"`
	Remark       string `json:"remark" binding:"max=2000"`
}

// UpdateFieldRequest 更新字段请求（整行编辑，含 id）
type UpdateFieldRequest struct {
	ID           uint64 `json:"id" binding:"required"`
	ProjectID    string `json:"project_id" binding:"required,max=100"`
	Name         string `json:"name" binding:"required,max=200"`
	Type         string `json:"type" binding:"required,oneof=int float string bool array object"`
	DefaultValue string `json:"default_value" binding:"required"`
	ParsePath    string `json:"parse_path" binding:"required,max=500"`
	Status       uint8  `json:"status" binding:"omitempty,oneof=1 2"`
	Remark       string `json:"remark" binding:"max=2000"`
}

// DeleteFieldRequest 删除字段请求
type DeleteFieldRequest struct {
	ID uint64 `json:"id" binding:"required"`
}

// FieldListRequest 字段列表请求
type FieldListRequest struct {
	Page      int    `form:"page" binding:"omitempty,min=1,max=10000"`
	PageSize  int    `form:"page_size" binding:"omitempty,min=1,max=100"`
	ProjectID string `form:"project_id" binding:"omitempty,max=100"`
	Name      string `form:"name" binding:"omitempty,max=200"`
	Type      string `form:"type" binding:"omitempty,oneof=int float string bool array object"`
	Status    *uint8 `form:"status" binding:"omitempty,oneof=1 2"`
}

// FieldQueryRequest 按主键查询（query 传 id）
type FieldQueryRequest struct {
	ID uint64 `form:"id" binding:"required"`
}

// FieldResponse 字段响应
type FieldResponse struct {
	ID            uint64 `json:"id"`
	ProjectID     string `json:"project_id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	TypeText      string `json:"type_text"`
	DefaultValue  string `json:"default_value"`
	ParsePath     string `json:"parse_path"`
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
func (f *Field) ToResponse() *FieldResponse {
	return &FieldResponse{
		ID:            f.ID,
		ProjectID:     f.ProjectID,
		Name:          f.Name,
		Type:          f.Type,
		TypeText:      FieldTypeLabel(f.Type),
		DefaultValue:  f.DefaultValue,
		ParsePath:     f.ParsePath,
		Status:        f.Status,
		StatusText:    ProjectStatusText(f.Status),
		Remark:        f.Remark,
		CreatedUser:   f.CreatedUser,
		UpdatedUser:   f.UpdatedUser,
		CreatedAt:     f.CreatedAt,
		CreatedAtText: formatUnix(f.CreatedAt),
		UpdatedAt:     f.UpdatedAt,
		UpdatedAtText: formatUnix(f.UpdatedAt),
	}
}
