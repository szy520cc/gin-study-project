package model

// FieldSnapshot 指标元信息快照（缓存内轻量版，不含审计字段）。
// 执行前按 bind_var 批量查指标元信息时用，序列化进 Redis 快照。
type FieldSnapshot struct {
	ID           uint64 `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	ParsePath    string `json:"parse_path"`
	DefaultValue string `json:"default_value"`
}

// RuleSnapshot 规则快照。
type RuleSnapshot struct {
	Script     string `json:"script"`      // 编译后 Starlark 成品脚本
	ResultType string `json:"result_type"` // 结果类型
}

// ConfigSnapshot 配置快照（一次 GET 拿全量：主表 + 规则 + 指标 + 灰度待上线版本）。
//
// 对齐资料的 ExtensionCache：执行面常规路径读一次快照即拿到全部求值所需信息，
// 灰度期间 NewVersion 装着待上线版本的全量快照（结构同外层），一个 key 装两份。
type ConfigSnapshot struct {
	Pack       string           `json:"pack"`
	Extension  string           `json:"extension"`
	Version    string           `json:"version"`
	Type       string           `json:"type"`
	Status     uint8            `json:"status"`
	CutNum     float64          `json:"cut_num"`
	CutVersion string           `json:"cut_version"`
	Rule       *RuleSnapshot    `json:"rule,omitempty"`
	BindVar    []*FieldSnapshot `json:"bind_var,omitempty"`
	NewVersion *ConfigSnapshot  `json:"new_version,omitempty"` // 灰度待上线版本（同结构）
}
