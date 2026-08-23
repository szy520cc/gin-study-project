// Package buildinfo 承载编译期注入的版本信息。
//
// 独立成包而不是放在 main：ldflags 的 -X 只能给存在的符号赋值，
// 对不存在的符号会被静默忽略。放在这里，server/migrate 等所有入口
// 共用同一个符号，注入一次即可，也不会因为改了 main 包而失效。
//
// 注入方式（见 Makefile / Dockerfile）：
//
//	go build -ldflags "-X myproject/pkg/buildinfo.Version=$(git describe --tags --always)"
package buildinfo

import "runtime"

// Version 编译期注入的版本号，未注入时为 dev
var Version = "dev"

// GoVersion 编译所用的 Go 版本
func GoVersion() string { return runtime.Version() }
