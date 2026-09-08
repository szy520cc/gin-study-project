package model

// Rule 规则配置，对应资料中的 uc_ext_rule。
//
// config 的 type=rule 时，规则内容存在此表（通过 config_id 关联 config 主表）。
// 遵循资料「编辑态/执行态分离」设计：
//   - Rule       存编译后的 Starlark 成品脚本（执行面直接跑）
//   - ConditionConfig 存占位符原文 JSON（管理面回显编辑器）
//   - BindVar    存逗号分隔的指标 ID（field.id），执行前按此批量查指标元信息
type Rule struct {
	ID              uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	ConfigID        uint64 `json:"config_id" gorm:"index;not null"`
	Rule            string `json:"rule" gorm:"type:text"`
	ConditionConfig string `json:"condition_config" gorm:"type:text"`
	BindVar         string `json:"bind_var" gorm:"type:varchar(500);not null;default:''"`
	ResultType      string `json:"result_type" gorm:"type:varchar(50);not null"`
	Engine          string `json:"engine" gorm:"type:varchar(50);not null;default:'starlark'"`
	Pack            string `json:"pack" gorm:"type:varchar(200);not null"`
	Extension       string `json:"extension" gorm:"type:varchar(200);not null"`
	Version         string `json:"version" gorm:"type:varchar(50);not null"`
	CreatedAt       int64  `json:"created_at" gorm:"type:int unsigned;not null;default:0"`
	UpdatedAt       int64  `json:"updated_at" gorm:"type:int unsigned;not null;default:0"`
}

// TableName 表名（单数 rule）
func (Rule) TableName() string { return "rule" }

// 规则结果类型
const (
	// ResultTypePassRejectReview 审核结果：通过/拒绝/送审（结果为 int）
	ResultTypePassRejectReview = "pass_reject_review"
	// ResultTypeHitOrNot 命中结果：命中/未命中（结果为 bool/int）
	ResultTypeHitOrNot = "hit_result"
	// ResultTypeJSON JSON 结果（结果为任意 JSON 反序列化值）
	ResultTypeJSON = "json"
)

// 执行引擎类型（本项目写死 starlark）
const (
	EngineStarlark = "starlark"
)

// ConfigTypeRule 配置类型标记：type=rule 的 config，其规则内容存 rule 表。
const ConfigTypeRule = "rule"

// 规则状态（沿用 config 的状态）
// 待审核 0 / 生效 1 / 下线 2，由 config 主表 status 承载，rule 不重复存 status。

// RuleConditionConfig 规则编辑态原文（condition_config 字段的 JSON 结构）。
// 前端编辑器写占位符 ##ID**解析路径##，保存时后端编译成 Starlark 并收集 bind_var。
type RuleConditionConfig struct {
	BindVar []int64 `json:"bind_var"` // 去重后的指标 ID 列表
	Rule    string  `json:"rule"`     // 含占位符的规则原文
}

// SaveRuleRequest 保存规则请求。
// 规则挂在 config（type=rule）下；编辑生效版本时会 fork 新版本，返回新 config 信息。
type SaveRuleRequest struct {
	ConfigID   uint64 `json:"config_id" binding:"required"`
	Rule       string `json:"rule" binding:"required"`                                        // 含占位符的规则原文
	ResultType string `json:"result_type" binding:"required,oneof=pass_reject_review hit_result json"`
}

// CreateRuleConfigRequest 一步创建「type=rule 配置 + 规则内容」请求（规则管理页「新增规则」）。
// 版本号由后端自动生成（时间戳），type 固定为 rule；logo 需为全新标识
// （同一标识的后续版本只能通过编辑已有版本 fork 产生）。
type CreateRuleConfigRequest struct {
	ProjectID    string `json:"project_id" binding:"required,max=100"`
	ConfigPackID uint64 `json:"config_pack_id" binding:"required"`
	Name         string `json:"name" binding:"required,max=200"`
	Logo         string `json:"logo" binding:"required,max=200"`
	Remark       string `json:"remark" binding:"max=2000"`
	Rule         string `json:"rule" binding:"required"` // 含占位符的规则原文
	ResultType   string `json:"result_type" binding:"required,oneof=pass_reject_review hit_result json"`
}

// RuleResponse 规则响应（含编译后脚本 + 原文 + 引用指标详情）。
type RuleResponse struct {
	ConfigID        uint64           `json:"config_id"`
	ProjectID       string           `json:"project_id"`       // 规则所属项目（编辑器按项目过滤字段用）
	Rule            string           `json:"rule"`             // 编译后 Starlark 成品
	RuleSource      string           `json:"rule_source"`      // 占位符原文（供编辑器回显）
	ConditionConfig string           `json:"condition_config"` // 占位符原文 JSON（含 bind_var）
	BindVar         string           `json:"bind_var"`         // 逗号分隔指标 ID
	BindVarInfo     []*FieldResponse `json:"bind_var_info"`    // 引用指标元信息
	ResultType      string           `json:"result_type"`
	Engine          string           `json:"engine"`
	Version         string           `json:"version"` // 所在 config 版本号
}

// TestRunRequest 现场验证请求（不落库、不碰缓存）。
// Data 目标参数：兼容 JSON 对象（{"material":{...}}）或 JSON 对象字符串两种形态，
// 由 service 统一规范化为 map（见 normalizeData）。
type TestRunRequest struct {
	Rule       string      `json:"rule" binding:"required"` // 规则原文（含占位符）
	ResultType string      `json:"result_type" binding:"required,oneof=pass_reject_review hit_result json"`
	Data       interface{} `json:"data"`                    // 目标参数（任意 JSON）
}

// TestRunResponse 现场验证结果。
// Value 是执行结果；BindVarInfo 列出每个指标的实际提取值，便于区分「规则写错」与「取值取错」。
type TestRunResponse struct {
	Value       interface{}      `json:"value"`
	ResultType  string           `json:"result_type"`
	BindVarInfo []*BindVarResult `json:"bind_var_info"`
}

// BindVarResult 单个指标的提取结果。
type BindVarResult struct {
	ID      uint64      `json:"id"`
	Name    string      `json:"name"`
	Path    string      `json:"parse_path"`
	Value   interface{} `json:"value"` // 实际提取值（取不到时为默认值）
	Hit     bool        `json:"hit"`   // 是否从输入数据中成功提取到
	Default interface{} `json:"default"`
}

// PublishRequest 全量发布请求（发布待审核版本，body 带 config id）。
type PublishRequest struct {
	ID uint64 `json:"id" binding:"required"`
}

// CutProgressRequest 灰度切流请求。
// ConfigID 是待上线版本（status=0）；CutNum 是切给新版本的流量比例 (0,1)。
type CutProgressRequest struct {
	ConfigID uint64  `json:"config_id" binding:"required"`
	CutNum   float64 `json:"cut_num" binding:"required"`
}

// EvalRequest 线下测试求值请求（供程序调用）。
// Pack 项目标识（project.logo），Key 配置标识（config.logo）。
// Version 显式指定版本时跳过灰度；OfflineFlag 为 true 时读草稿（草稿优先）。
// Data 目标参数：兼容 JSON 对象或 JSON 对象字符串，service 统一规范化。
type EvalRequest struct {
	Pack        string      `json:"pack" binding:"required"`
	Key         string      `json:"key" binding:"required"`
	Version     string      `json:"version"`
	OfflineFlag bool        `json:"offline_flag"`
	Data        interface{} `json:"data"` // 目标参数；规则不引用指标时可为空
}

// EvalResponse 求值响应。
type EvalResponse struct {
	Pack       string      `json:"pack"`
	Key        string      `json:"key"`
	Version    string      `json:"version"` // 实际执行的版本号
	Value      interface{} `json:"value"`   // 执行结果
	ResultType string      `json:"result_type"`
}
