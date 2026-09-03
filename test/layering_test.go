package test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// TestLayering 守住分层边界：SQL 只能出现在 internal/data。
//
// 这条约束靠约定守不住 —— service 只要 import 一下 gorm 就能自己拼查询，
// 时间一长数据访问代码就会散回业务层。这里直接扫 import 表，
// 违反时在 CI 里就红，不用等 review 发现。
func TestLayering(t *testing.T) {
	cases := []struct {
		dir    string
		forbid []string
		why    string
	}{
		{
			dir:    "../internal/service",
			forbid: []string{"gorm.io/gorm", "myproject/internal/resource"},
			why:    "service 层不写 SQL：数据访问走 internal/data 的包级函数",
		},
		{
			dir:    "../internal/controller",
			forbid: []string{"gorm.io/gorm", "myproject/internal/data"},
			why:    "controller 层不碰数据库：只做绑参、调 service、写响应",
		},
		{
			dir:    "../internal/data",
			forbid: []string{"myproject/pkg/errcode", "github.com/gin-gonic/gin"},
			why:    "data 层不认识业务错误与 HTTP：只返回 gorm 原始错误",
		},
	}

	for _, c := range cases {
		files, err := filepath.Glob(filepath.Join(c.dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("扫描 %s 失败：err=%v files=%d", c.dir, err, len(files))
		}

		for _, file := range files {
			f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("解析 %s 失败：%v", file, err)
			}
			for _, spec := range f.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("解析 %s 的 import 失败：%v", file, err)
				}
				for _, bad := range c.forbid {
					if path == bad {
						t.Errorf("%s 不应该 import %q —— %s", file, bad, c.why)
					}
				}
			}
		}
	}
}
