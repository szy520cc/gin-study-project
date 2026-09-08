package engine

import (
	"encoding/json"
	"strings"
)

// FieldMeta 指标元信息（执行时按此提取字段值）。
// 对应 field 表，但 engine 不 import model，由调用方把 model.Field 转成此结构，
// 避免纯计算包与业务 model 耦合。
type FieldMeta struct {
	ID          uint64 // 指标 ID
	Name        string // 指标名称
	Type        string // 类型：int/float/string/bool/array/object
	ParsePath   string // 解析路径（点路径），如 material.vertical_type
	DefaultValue string // 默认值配置 JSON，如 {"type":"int","value":-1}
}

// EnvKey 生成该指标在执行上下文里的 key（与编译器 CompileRule 生成的 key 一致）。
// 例：parse_path=material.vertical_type, id=209 → material$vertical_type$209
func (f FieldMeta) EnvKey() string {
	return NormalizePath(f.ParsePath) + "$" + Uint64ToStr(f.ID)
}

// Uint64ToStr uint64 转十进制字符串（避免 fmt 的额外开销与 import）。
func Uint64ToStr(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// fieldDefault 字段默认值配置结构，对应 field.default_value 的 JSON。
type fieldDefault struct {
	Type  string      `json:"type"`
	Value interface{} `json:"value"`
}

// ParseDefaultValue 解析默认值配置 JSON，返回默认值（失败返回 nil）。
func ParseDefaultValue(raw string) interface{} {
	if raw == "" {
		return nil
	}
	var d fieldDefault
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil
	}
	return d.Value
}

// ExtractByPath 按点路径从输入数据中提取字段值。
// 返回 (value, found)。found=false 表示路径不存在或中间节点不是对象。
//
// 例：data={"material":{"vertical_type":5}}，path="material.vertical_type" → (5, true)
func ExtractByPath(data map[string]any, path string) (interface{}, bool) {
	if data == nil || path == "" {
		return nil, false
	}
	parts := strings.Split(path, ".")
	var cur interface{} = data
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// ResolveFieldValue 提取字段值，取不到时用默认值兜底。
// 这是执行前组装 env map 的核心：提取、类型、兜底一步完成。
func ResolveFieldValue(data map[string]any, f FieldMeta) interface{} {
	if v, ok := ExtractByPath(data, f.ParsePath); ok {
		return v
	}
	return ParseDefaultValue(f.DefaultValue)
}
