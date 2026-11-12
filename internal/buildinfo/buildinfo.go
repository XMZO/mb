// Package buildinfo 保存构建时通过 -ldflags 注入的版本信息。
package buildinfo

import "strings"

// 由构建脚本注入:
//
//	-X mb/internal/buildinfo.Version=v1.2.3 -X mb/internal/buildinfo.Commit=<sha>
var (
	Version = "dev"
	Commit  = ""
)

// DisplayVersion 返回用于展示的版本号。
func DisplayVersion() string {
	if v := strings.TrimSpace(Version); v != "" {
		return v
	}
	return "dev"
}

// ShortCommit 返回截断到 12 位的提交哈希,未注入时为空。
func ShortCommit() string {
	c := strings.TrimSpace(Commit)
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
