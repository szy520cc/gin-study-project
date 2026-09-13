package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// ResultType 常量与 model 保持一致（engine 不 import model，用字面量判断）。
const (
	// ResultTypeJSON JSON 结果：脚本 result 为 json.dumps 后的字符串。
	ResultTypeJSON = "json"
)

// ToStarlarkValue 把 Go 值递归转成 Starlark 值。
// 支持 nil/bool/int*/float64/string/[]interface{}/map[string]interface{}；
// 其它类型走 JSON 往返兜底（gin binding 的 JSON 数字是 float64）。
func ToStarlarkValue(v interface{}) (starlark.Value, error) {
	switch t := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(t), nil
	case int:
		return starlark.MakeInt(t), nil
	case int64:
		return starlark.MakeInt64(t), nil
	case int32:
		return starlark.MakeInt(int(t)), nil
	case uint64:
		return starlark.MakeUint64(t), nil
	case float64:
		return starlark.Float(t), nil
	case float32:
		return starlark.Float(float64(t)), nil
	case string:
		return starlark.String(t), nil
	case []interface{}:
		elems := make([]starlark.Value, 0, len(t))
		for _, e := range t {
			sv, err := ToStarlarkValue(e)
			if err != nil {
				return nil, err
			}
			elems = append(elems, sv)
		}
		return starlark.NewList(elems), nil
	case map[string]interface{}:
		d := starlark.NewDict(len(t))
		for k, e := range t {
			sv, err := ToStarlarkValue(e)
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), sv); err != nil {
				return nil, err
			}
		}
		return d, nil
	default:
		// 兜底：JSON 往返归一化（处理 []map 等 gin binding 不会产生、但测试可能构造的类型）
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("无法转换类型 %T: %w", v, err)
		}
		var generic interface{}
		if err := json.Unmarshal(b, &generic); err != nil {
			return nil, err
		}
		return ToStarlarkValue(generic)
	}
}

// ToStarlarkDict 把 env map 转成 Starlark Dict，作为 context 全局变量注入脚本。
func ToStarlarkDict(env map[string]any) (*starlark.Dict, error) {
	d := starlark.NewDict(len(env))
	for k, v := range env {
		sv, err := ToStarlarkValue(v)
		if err != nil {
			return nil, fmt.Errorf("字段 %s: %w", k, err)
		}
		if err := d.SetKey(starlark.String(k), sv); err != nil {
			return nil, fmt.Errorf("字段 %s: %w", k, err)
		}
	}
	return d, nil
}

// Run 执行 Starlark 脚本，返回 result 全局变量的 Starlark 值。
//
// 约定：脚本必须给全局变量 result 赋值（如 `result = judge()`）。
// 两个编译选项与资料一致：TopLevelControl 允许顶层 if/for，GlobalReassign 允许重赋值。
// maxExecSteps Starlark 单次执行步数上限：拦截误写死循环/超长递归的规则，
// 防止打满 CPU 拖垮服务（timeout 中间件兜底的是整体请求，这里是语言层护栏）。
const maxExecSteps = 1_000_000

func Run(script string, env map[string]any) (starlark.Value, error) {
	contextDict, err := ToStarlarkDict(env)
	if err != nil {
		return nil, err
	}
	globals := starlark.StringDict{
		"context": contextDict,
		"json":    jsonModule(),
	}

	thread := &starlark.Thread{Name: "rule"}
	thread.SetMaxExecutionSteps(maxExecSteps)

	options := syntax.LegacyFileOptions()
	options.TopLevelControl = true
	options.GlobalReassign = true

	globals, err = starlark.ExecFileOptions(options, thread, "rule.star", script, globals)
	if err != nil {
		return nil, fmt.Errorf("执行脚本失败: %w", err)
	}

	result, ok := globals["result"]
	if !ok {
		return nil, fmt.Errorf("脚本未给 result 赋值（约定脚本末尾需有 result = ...）")
	}
	return result, nil
}

// ValidateStarlark 对「编译后的成品脚本」做 Starlark 解析 + 名字解析 + 编译校验，不执行。
//
// 与 Run 的唯一差别是不做 Program.Init（即不跑脚本），因此能提前拦住：
//   - 语法错误（括号不配对、缩进错、缺冒号、字符串未闭合等）
//   - 引用了未定义的名字（预声明名与执行环境保持一致：context / json）
//   - 结果类型不唯一：同一函数混用 return True / return 1，或顶层 result
//     被赋成多种类型（见 validateValueTypes）
//
// 保存（新增/编辑）与切流/发布前的校验都走这里，
// 保证「未通过校验的规则不会入库、不会上线」。
//
// 报错会被格式化成「第 N 行：<中文说明>」，直接展示给运营。
func ValidateStarlark(script string) error {
	options := syntax.LegacyFileOptions()
	options.TopLevelControl = true
	options.GlobalReassign = true
	isPredeclared := func(name string) bool { return name == "context" || name == "json" }
	file, _, err := starlark.SourceProgramOptions(options, "rule.star", script, isPredeclared)
	if err != nil {
		return errors.New(formatStarlarkError(err))
	}
	return validateValueTypes(file)
}

// starlarkPosRe 匹配 go.starlark.net 的报错前缀：`rule.star:行:列: 具体说明`。
//
// 注意：**只取行号，不取列号**。校验跑的是「编译后的执行态脚本」，
// 占位符 `##209**a.b##` 会展开成 `context["a$b$209"]` 把行拉长，
// 列号随之右移（实测源码第 25 列 → 编译后第 31 列），直接展示会把人带偏。
// 行号不受影响：占位符不会引入换行，编译前后行号一一对应。
var starlarkPosRe = regexp.MustCompile(`rule\.star:(\d+):(\d+):\s*(.*)`)

// starlarkErrHints 把编译器的高频报错翻成中文。
//
// 顺序有意义：更具体的规则必须排在更宽泛的规则前面
// （例如 `got end of file, want ')'` 会同时命中通用的 `got X, want 'Y'`）。
// 全部命中不了时原样返回英文原文 —— 保留原文比猜错更有用。
var starlarkErrHints = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`^return statement not within a function$`),
		"return 只能写在函数内部（Starlark 不允许函数外的 return）：请用 def 定义函数，并在脚本末尾给 result 赋值"},
	{regexp.MustCompile(`^undefined: (\w+)$`),
		"使用了未定义的名称「$1」"},
	{regexp.MustCompile(`^unexpected EOF in string$`),
		"字符串没有闭合（缺少收尾的引号）"},
	{regexp.MustCompile(`^got end of file, want '(.+)'$`),
		"脚本提前结束，此处应补上 '$1'"},
	{regexp.MustCompile(`^got newline, want '(.+)'$`),
		"此处应补上 '$1'（该行提前换行了）"},
	{regexp.MustCompile(`^got (.+), want '(.+)'$`),
		"此处应为 '$2'，实际是 $1"},
}

// formatStarlarkError 把 `rule.star:2:1: got newline, want ':'`
// 转成 `第 2 行：此处应补上 ':'（该行提前换行了）`。
func formatStarlarkError(err error) string {
	raw := err.Error()
	m := starlarkPosRe.FindStringSubmatch(raw)
	if m == nil {
		return raw
	}
	detail := m[3]
	for _, h := range starlarkErrHints {
		if h.re.MatchString(detail) {
			detail = h.re.ReplaceAllString(detail, h.repl)
			break
		}
	}
	return fmt.Sprintf("第 %s 行：%s", m[1], detail)
}

// ConvertResult 把 Starlark 结果值按 resultType 转成 Go 值。
//
//   - resultType == "json"：result 须为字符串（json.dumps 产物），反序列化为 Go 值；
//   - 其它：bool 原样返回；否则按整数（AsInt32）返回（审核 0/1/2、命中 0/1）。
func ConvertResult(v starlark.Value, resultType string) (interface{}, error) {
	if resultType == ResultTypeJSON {
		s, ok := v.(starlark.String)
		if !ok {
			return nil, fmt.Errorf("JSON 结果要求脚本 result 为字符串（json.dumps(...)），实际为 %s", v.Type())
		}
		var out interface{}
		if err := json.Unmarshal([]byte(s.GoString()), &out); err != nil {
			return nil, fmt.Errorf("解析 JSON 结果失败: %w", err)
		}
		return out, nil
	}

	if b, ok := v.(starlark.Bool); ok {
		return bool(b), nil
	}
	i, err := starlark.AsInt32(v)
	if err != nil {
		return nil, fmt.Errorf("结果类型转换失败（期望整数，实际 %s）: %w", v.Type(), err)
	}
	return int(i), nil
}

// Execute 完整执行一条规则：组装 env（按指标提取字段值）→ 跑 Starlark → 转换结果。
// 这是「规则执行」的统一入口，TestRun 与 eval 共用，保证两条路径行为一致。
func Execute(script, resultType string, fields []FieldMeta, data map[string]any) (interface{}, error) {
	env := make(map[string]any, len(fields))
	for _, f := range fields {
		env[f.EnvKey()] = ResolveFieldValue(data, f)
	}
	result, err := Run(script, env)
	if err != nil {
		return nil, err
	}
	return ConvertResult(result, resultType)
}

// jsonModule 返回注入到 Starlark 环境的 json 模块，供脚本调用 json.dumps。
//
// Starlark 标准库不含 json，资料里的 `result = json.dumps(...)` 依赖此注入。
func jsonModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "json",
		Members: starlark.StringDict{
			"dumps": starlark.NewBuiltin("json.dumps", jsonDumps),
		},
	}
}

// jsonDumps 把 Starlark 值序列化成 JSON 字符串（json.dumps 的内置实现）。
func jsonDumps(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var x starlark.Value
	if err := starlark.UnpackPositionalArgs("json.dumps", args, kwargs, 1, &x); err != nil {
		return nil, err
	}
	gv, err := starlarkToGo(x)
	if err != nil {
		return nil, fmt.Errorf("json.dumps: %w", err)
	}
	data, err := json.Marshal(gv)
	if err != nil {
		return nil, fmt.Errorf("json.dumps: %w", err)
	}
	return starlark.String(data), nil
}

// starlarkToGo 把 Starlark 值递归转回 Go 值（ToStarlarkValue 的逆过程）。
// 仅用于 json.dumps 输出；Dict 的 key 假定为字符串。
func starlarkToGo(v starlark.Value) (interface{}, error) {
	switch t := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(t), nil
	case starlark.Int:
		if i, ok := t.Int64(); ok {
			return i, nil
		}
		return t.String(), nil // 超出 int64 的大整数退化为字符串，避免精度丢失
	case starlark.Float:
		return float64(t), nil
	case starlark.String:
		return string(t), nil
	case *starlark.Dict:
		m := make(map[string]interface{}, t.Len())
		for _, k := range t.Keys() {
			s, ok := starlark.AsString(k)
			if !ok {
				continue
			}
			val, _, err := t.Get(k)
			if err != nil {
				continue
			}
			gv, err := starlarkToGo(val)
			if err != nil {
				return nil, err
			}
			m[s] = gv
		}
		return m, nil
	case *starlark.List:
		items := make([]interface{}, 0, t.Len())
		for i := 0; i < t.Len(); i++ {
			gv, err := starlarkToGo(t.Index(i))
			if err != nil {
				return nil, err
			}
			items = append(items, gv)
		}
		return items, nil
	case starlark.Tuple:
		items := make([]interface{}, 0, t.Len())
		for i := 0; i < t.Len(); i++ {
			gv, err := starlarkToGo(t.Index(i))
			if err != nil {
				return nil, err
			}
			items = append(items, gv)
		}
		return items, nil
	default:
		return v.String(), nil
	}
}
