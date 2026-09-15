package engine

import (
	"fmt"

	"go.starlark.net/syntax"
)

// 本文件负责「结果类型唯一性」静态校验。
//
// 背景：规则结果按 result_type 转换（见 ConvertResult）——json 要求字符串，
// 否则 bool 原样返回、其余按整数返回。若同一份规则在不同分支返回不同类型
// （典型：`if ...: return True` 与 `return 1` 并存），线上就会时而得到 bool、
// 时而得到 int，调用方无法按固定契约消费。因此约定两条硬规矩：
//
//  1. 同一个函数内所有 return 的取值类型必须一致；
//  2. 顶层对 result 的多次赋值，类型也必须一致。
//
// 只对「能静态确定的字面量」判定：变量、函数调用、算术运算等一律放行，
// 宁可漏报也不误报；运行期的类型兜底仍然由 ConvertResult 负责
// （而保存前的真实试跑会把它暴露出来）。

// literalTypeName 返回表达式的静态类型名，第二个返回值表示能否确定。
//
// 覆盖范围刻意保守：只有当表达式本身就把类型写死时才返回 true。
func literalTypeName(e syntax.Expr) (string, bool) {
	if e == nil {
		return "none", true // 裸 return 等价于返回 None
	}
	switch t := e.(type) {
	case *syntax.Ident:
		// go.starlark.net 把 True / False / None 解析成预声明标识符，而不是字面量节点，
		// 所以这里必须显式识别（其它标识符是变量，类型未知，放行）。
		switch t.Name {
		case "True", "False":
			return "bool", true
		case "None":
			return "none", true
		}
	case *syntax.Literal:
		switch t.Value.(type) {
		case bool:
			return "bool", true
		case int64:
			return "int", true
		case float64:
			return "float", true
		case string:
			return "string", true
		case nil:
			return "none", true
		}
	case *syntax.ListExpr:
		return "list", true
	case *syntax.DictExpr:
		return "dict", true
	case *syntax.TupleExpr:
		return "tuple", true
	case *syntax.UnaryExpr:
		if t.Op == syntax.NOT {
			return "bool", true
		}
		return literalTypeName(t.X) // -1 / +1 之类取操作数类型
	case *syntax.CondExpr:
		// x if cond else y：只有两支类型相同且都可确定时才判定
		a, aok := literalTypeName(t.True)
		b, bok := literalTypeName(t.False)
		if aok && bok && a == b {
			return a, true
		}
	}
	return "", false
}

// typeGuard 逐个登记「可静态确定的类型」，一旦出现第二种就记下错误。
type typeGuard struct {
	desc      string // 场景描述，用于报错文案
	first     string
	firstLine int32
	err       error
}

func (g *typeGuard) add(name string, line int32) {
	if g.err != nil {
		return
	}
	if g.first == "" {
		g.first, g.firstLine = name, line
		return
	}
	if name != g.first {
		g.err = fmt.Errorf(
			"第 %d 行：%s 与第 %d 行的结果类型不一致（这里返回 %s，第 %d 行返回 %s）。"+
				"同一份规则的结果类型必须唯一，不能混用 bool / int / float / string / list / dict",
			line, g.desc, g.firstLine, name, g.firstLine, g.first)
	}
}

// collectReturns 收集函数体内所有 return。
// 刻意不进入嵌套 def：嵌套函数由它自己那一轮校验负责，避免类型池被搅在一起。
func collectReturns(stmts []syntax.Stmt, out *[]*syntax.ReturnStmt) {
	for _, s := range stmts {
		switch t := s.(type) {
		case *syntax.ReturnStmt:
			*out = append(*out, t)
		case *syntax.IfStmt:
			collectReturns(t.True, out)
			collectReturns(t.False, out)
		case *syntax.ForStmt:
			collectReturns(t.Body, out)
		case *syntax.DefStmt:
			// 跳过：嵌套函数单独校验
		}
	}
}

// collectResultAssigns 收集顶层（函数外）对 result 的赋值。
// 函数体内的 result 是局部变量，不影响最终结果，因此不进入 def。
func collectResultAssigns(stmts []syntax.Stmt, out *[]*syntax.AssignStmt) {
	for _, s := range stmts {
		switch t := s.(type) {
		case *syntax.AssignStmt:
			if t.Op != syntax.EQ {
				continue // 只认纯赋值（= 是 EQ，== 是 EQL）；+= 之类由运行期兜底
			}
			if id, ok := t.LHS.(*syntax.Ident); ok && id.Name == "result" {
				*out = append(*out, t)
			}
		case *syntax.IfStmt:
			collectResultAssigns(t.True, out)
			collectResultAssigns(t.False, out)
		case *syntax.ForStmt:
			collectResultAssigns(t.Body, out)
		case *syntax.DefStmt:
			// 跳过：函数体内的 result 是局部的
		}
	}
}

// validateValueTypes 做「结果类型唯一性」校验，是 ValidateStarlark 的第三层。
// 返回 nil 表示类型层面没有可静态发现的冲突。
func validateValueTypes(file *syntax.File) error {
	// 1) 顶层 result 赋值必须同类型
	var assigns []*syntax.AssignStmt
	collectResultAssigns(file.Stmts, &assigns)
	if len(assigns) > 1 {
		g := &typeGuard{desc: "顶层 result"}
		for _, a := range assigns {
			if name, ok := literalTypeName(a.RHS); ok {
				g.add(name, a.OpPos.Line)
			}
		}
		if g.err != nil {
			return g.err
		}
	}

	// 2) 每个函数内所有 return 必须同类型
	var bad error
	syntax.Walk(file, func(n syntax.Node) bool {
		if bad != nil {
			return false
		}
		def, ok := n.(*syntax.DefStmt)
		if !ok {
			return true
		}
		var rets []*syntax.ReturnStmt
		collectReturns(def.Body, &rets)
		if len(rets) < 2 {
			return true
		}
		g := &typeGuard{desc: fmt.Sprintf("函数 %s", def.Name.Name)}
		for _, r := range rets {
			if name, ok := literalTypeName(r.Result); ok {
				g.add(name, r.Return.Line)
			}
		}
		if g.err != nil {
			bad = g.err
		}
		return true
	})
	return bad
}
