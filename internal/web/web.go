// Package web 持有后台管理 UI 的前端资产。
//
// 为什么不直接放在仓库顶层 web/ 目录：
//
//	//go:embed 只能引用「同一 package 源码所在目录及其子目录」，
//	而 router 包在 internal/router/ 下，无法 embed 顶层 web/。
//	把资产挪进 internal/web/assets/ 后，embed 一行就搞定。
//
// 为什么不放进 pkg/web/：
//
//	这些资产是后端的内部组成（CORS、鉴权、JWT 都在同一进程提供），
//	没有「公开给第三方 import 嵌入二进制」的需求。internal 反而准确。
//
// 不暴露 http.FileSystem 句柄：embed.FS 的零散方法会让调用方
// 误用（比如有人会用 http.FS(assets) 直接传 gin——但我们要的是
// gin 静态服务的细粒度控制，比如只挂 static、pages 走单独的 handler，
// 不让误放的 yaml/json 文件被 StaticFS 一锅端）。
// 改用三个有明确语义的方法。
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:assets
var assets embed.FS

// rootFS 切掉顶层 "assets" 前缀，让调用方写 "index.html" 而不是 "assets/index.html"。
//
// 顶层保留一个 FS 句柄在外部包里是常见的：避免每个方法重新 fs.Sub。
var rootFS, _ = fs.Sub(assets, "assets")

// ReadIndex 返回后台入口 HTML。
//
// 路径故意写 "index.html" 而不是 "/"：
// embed.FS 不支持目录索引，必须显式指定文件。
func ReadIndex() ([]byte, error) { return assets.ReadFile("assets/index.html") }

// ReadLogin 返回登录页 HTML。单独成一个页是见 web/index.html / login.html
// 的分离说明。
func ReadLogin() ([]byte, error) { return assets.ReadFile("assets/login.html") }

// ReadSchema 返回 web/pages/ 下的 amis schema JSON。
//
// 文件名必须包含 ".json" 前缀，例如 "/login.json"。
func ReadSchema(name string) ([]byte, error) {
	return assets.ReadFile("assets/pages" + name)
}

// StaticFS 返回静态资源子 FS（包含 sdk.js、iconify.min.js 等）。
// 配合 gin.StaticFS("/admin/static", http.FS(web.StaticFS())) 使用。
func StaticFS() fs.FS {
	sub, err := fs.Sub(rootFS, "static")
	if err != nil {
		// fs.Sub 在 embed 上失败只可能是写错路径名；嵌入期已知结构，不会到这里。
		panic(fmt.Sprintf("web: sub static: %v", err))
	}
	return sub
}
