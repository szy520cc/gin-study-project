// Package engine 是 Starlark 规则引擎的纯计算核心：编译、字段提取、执行。
//
// 本包不依赖 internal 的 controller/service/data 层，只依赖标准库与
// go.starlark.net，因此可以独立单元测试。DB 查询、事务、HTTP 编排都在上层做。
//
// 设计对齐资料（Starlark 规则执行引擎）的「编辑态 / 执行态分离」：
//   - 前端编辑器写占位符 `##指标ID**解析路径##`；
//   - 保存时 CompileRule 编译成 `context["{normalize(路径)}${id}"]` 成品脚本，
//     并收集去重后的指标 ID（bind_var）；
//   - 执行时按指标 parse_path 从输入数据提取值，注入 context，跑 Starlark。
package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// metricPattern 匹配占位符 `##指标ID**解析路径##`。
// 例：##209**material.vertical_type## 捕获 $1=209, $2=material.vertical_type
var metricPattern = regexp.MustCompile(`##(\d+)\*\*([\w.-]+)##`)

// NormalizePath 把解析路径里的点替换成 $，作为执行上下文 key 的一部分。
// 例：material.vertical_type → material$vertical_type
//
// 这是「编辑态路径 → 执行态 key」的唯一转换规则，编译与字段提取两处共用，
// 必须保持一致，否则脚本里的 context["..."] 会取不到值（静默拿到默认值）。
func NormalizePath(path string) string {
	return strings.ReplaceAll(path, ".", "$")
}

// CompileRule 把含占位符的规则原文编译成 Starlark 成品脚本，并收集去重后的指标 ID。
//
// 输入：`if ##209**material.vertical_type## in (0,1,5): return 1`
// 输出：`if context["material$vertical_type$209"] in (0,1,5): return 1`，bindVar=[209]
//
// 编译过程顺带完成「规则 → 依赖分析」：收集所有被引用的指标 ID 去重后返回，
// 调用方据此批量查指标元信息，无需再解析脚本。
func CompileRule(source string) (rule string, bindVar []int64) {
	var ids []int64
	replaced := metricPattern.ReplaceAllStringFunc(source, func(match string) string {
		sub := metricPattern.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		id, err := strconv.ParseInt(sub[1], 10, 64)
		if err != nil {
			return match
		}
		ids = append(ids, id)
		// 占位符 → context["path$id"]；路径只含 [\w.-]，无需转义
		return `context["` + NormalizePath(sub[2]) + `$` + sub[1] + `"]`
	})
	return replaced, dedupInt64(ids)
}

// dedupInt64 保持顺序去重。
func dedupInt64(in []int64) []int64 {
	seen := make(map[int64]struct{}, len(in))
	out := make([]int64, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// JoinInt64Slice 把指标 ID 列表拼成逗号分隔字符串（用于 rule.bind_var）。
func JoinInt64Slice(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

// SplitInt64Slice 把逗号分隔字符串拆成指标 ID 列表（忽略非法项）。
func SplitInt64Slice(s string) []int64 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// ValidateRule 对规则原文做基础校验（占位符格式是否合法）。
// 返回错误信息，空串表示通过。与 CompileRule 解耦：编译前先校验，
// 避免把明显写错的占位符（如 ##abc**x##）静默留在脚本里。
func ValidateRule(source string) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("规则内容不能为空")
	}
	// 找到所有形如 ##...## 的片段，非法的报错
	bad := regexp.MustCompile(`##[^#]*##`)
	for _, m := range bad.FindAllString(source, -1) {
		if !metricPattern.MatchString(m) {
			return fmt.Errorf("规则占位符格式错误：%s（应为 ##指标ID**解析路径##）", m)
		}
	}
	return nil
}
