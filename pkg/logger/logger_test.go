package logger

import (
	"reflect"
	"testing"
)

// TestFieldsToArgs 验证 fieldsToArgs 的边界处理与字段排序。
//
// 关键断言是「单个 map 内部按 key 字典序」：Go 的 map 遍历顺序随机，
// 不排序的话同一句日志每次输出顺序都不同，日志 diff 会抖动。
// 多个 map 之间仍保持调用方传参顺序，只有单个 map 内部排序。
func TestFieldsToArgs(t *testing.T) {
	cases := []struct {
		name   string
		fields []map[string]interface{}
		want   []any
	}{
		{"nil 入参", nil, nil},
		{"空切片", []map[string]interface{}{}, nil},
		{"含 nil map 不 panic", []map[string]interface{}{nil, map[string]interface{}{"a": 1}}, []any{"a", 1}},
		{"单 map 按 key 字典序", []map[string]interface{}{map[string]interface{}{"name": "x", "age": 20}}, []any{"age", 20, "name", "x"}},
		{"多 map 保持传参顺序，各自内部排序",
			[]map[string]interface{}{
				map[string]interface{}{"b": 2, "a": 1},
				map[string]interface{}{"d": 4, "c": 3},
			},
			[]any{"a", 1, "b", 2, "c", 3, "d", 4}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fieldsToArgs(c.fields)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("fieldsToArgs(%v) = %v, want %v", c.fields, got, c.want)
			}
		})
	}
}
