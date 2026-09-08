package engine

import (
	"testing"
)

// TestCompileRule 验证占位符编译成 Starlark 成品脚本 + 收集指标 ID。
func TestCompileRule(t *testing.T) {
	src := "def judge():\n" +
		"  if ##209**material.vertical_type## in (0,1,5) and ##216**material.category_v4## in (\"社会\",\"体育\"):\n" +
		"    return 1\n" +
		"  return 0\n" +
		"result = judge()"

	rule, bindVar := CompileRule(src)

	wantRule := "def judge():\n" +
		"  if context[\"material$vertical_type$209\"] in (0,1,5) and context[\"material$category_v4$216\"] in (\"社会\",\"体育\"):\n" +
		"    return 1\n" +
		"  return 0\n" +
		"result = judge()"

	if rule != wantRule {
		t.Errorf("编译结果不符\n got: %s\nwant: %s", rule, wantRule)
	}
	if len(bindVar) != 2 || bindVar[0] != 209 || bindVar[1] != 216 {
		t.Errorf("bind_var 收集错误: %v", bindVar)
	}

	// 重复引用同一指标应去重
	src2 := "result = 1 if ##209**a.b## == ##209**a.b## else 0"
	_, bindVar2 := CompileRule(src2)
	if len(bindVar2) != 1 {
		t.Errorf("去重失败: %v", bindVar2)
	}
}

// TestValidateRule 验证占位符格式校验。
func TestValidateRule(t *testing.T) {
	if err := ValidateRule(""); err == nil {
		t.Error("空规则应报错")
	}
	if err := ValidateRule("##abc**x##"); err == nil {
		t.Error("非法占位符应报错")
	}
	if err := ValidateRule("result = 1 if ##209**a.b## > 0 else 0"); err != nil {
		t.Errorf("合法占位符不应报错: %v", err)
	}
}

// TestExtractByPath 验证点路径提取。
func TestExtractByPath(t *testing.T) {
	data := map[string]any{
		"material": map[string]any{
			"vertical_type": float64(5),
			"category_v4":   "社会",
		},
		"author": map[string]any{"level": float64(3)},
	}

	if v, ok := ExtractByPath(data, "material.vertical_type"); !ok || v != float64(5) {
		t.Errorf("提取失败: %v %v", v, ok)
	}
	if v, ok := ExtractByPath(data, "material.category_v4"); !ok || v != "社会" {
		t.Errorf("提取失败: %v %v", v, ok)
	}
	if _, ok := ExtractByPath(data, "material.not_exist"); ok {
		t.Error("不存在路径应返回 false")
	}
	if _, ok := ExtractByPath(data, "material.vertical_type.deep"); ok {
		t.Error("中间非对象应返回 false")
	}
}

// TestRunAndConvert 完整链路：编译 → 提取 → 执行 → 结果转换。
// 这是核心冒烟，验证 Starlark 引擎真正能跑通审核规则。
func TestRunAndConvert(t *testing.T) {
	src := "def judge():\n" +
		"  if ##209**material.vertical_type## in (0,1,5) and ##216**material.category_v4## in (\"社会\",\"体育\",\"财经\"):\n" +
		"    return 1\n" +
		"  return 0\n" +
		"result = judge()"

	rule, bindVar := CompileRule(src)
	if len(bindVar) != 2 {
		t.Fatalf("bind_var 收集错误: %v", bindVar)
	}

	fields := []FieldMeta{
		{ID: 209, ParsePath: "material.vertical_type", DefaultValue: `{"type":"int","value":-1}`},
		{ID: 216, ParsePath: "material.category_v4", DefaultValue: `{"type":"string","value":""}`},
	}

	// 命中场景：vertical_type=1, category=社会 → return 1
	env := buildEnv(t, fields, map[string]any{
		"material": map[string]any{"vertical_type": float64(1), "category_v4": "社会"},
	})
	result, err := Run(rule, env)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	v, err := ConvertResult(result, "pass_reject_review")
	if err != nil {
		t.Fatalf("结果转换失败: %v", err)
	}
	if v != int(1) {
		t.Errorf("期望 1，实际 %v(%T)", v, v)
	}

	// 未命中场景：vertical_type=14 → return 0
	env2 := buildEnv(t, fields, map[string]any{
		"material": map[string]any{"vertical_type": float64(14), "category_v4": "社会"},
	})
	result2, err := Run(rule, env2)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	v2, _ := ConvertResult(result2, "pass_reject_review")
	if v2 != int(0) {
		t.Errorf("期望 0，实际 %v", v2)
	}
}

// TestDefaultValueFallback 验证字段缺失时用默认值兜底。
func TestDefaultValueFallback(t *testing.T) {
	f := FieldMeta{ID: 209, ParsePath: "material.vertical_type", DefaultValue: `{"type":"int","value":-1}`}
	data := map[string]any{} // 空数据，vertical_type 缺失
	v := ResolveFieldValue(data, f)
	if v != float64(-1) {
		t.Errorf("默认值兜底失败，期望 -1，实际 %v", v)
	}
}

// TestJSONResult 验证 JSON 结果类型。
func TestJSONResult(t *testing.T) {
	src := "result = json.dumps({\"ok\": True, \"n\": 3})"
	result, err := Run(src, map[string]any{})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	v, err := ConvertResult(result, ResultTypeJSON)
	if err != nil {
		t.Fatalf("结果转换失败: %v", err)
	}
	m, ok := v.(map[string]interface{})
	if !ok || m["ok"] != true {
		t.Errorf("JSON 结果解析失败: %v", v)
	}
}

// TestRunInfiniteLoop 验证执行步数上限拦截死循环脚本（防打满 CPU）。
func TestRunInfiniteLoop(t *testing.T) {
	src := "i = 0\nwhile True:\n  i = i + 1\nresult = 0"
	if _, err := Run(src, map[string]any{}); err == nil {
		t.Error("死循环脚本应触发执行步数上限报错")
	}
}

// TestRunNoResult 验证脚本未给 result 赋值时报错。
func TestRunNoResult(t *testing.T) {
	if _, err := Run("x = 1", map[string]any{}); err == nil {
		t.Error("未给 result 赋值应报错")
	}
}

func buildEnv(t *testing.T, fields []FieldMeta, data map[string]any) map[string]any {
	t.Helper()
	env := make(map[string]any, len(fields))
	for _, f := range fields {
		env[f.EnvKey()] = ResolveFieldValue(data, f)
	}
	return env
}
