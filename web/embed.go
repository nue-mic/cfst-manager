// Package web 内嵌前端静态资源（dist 目录），随二进制一起分发。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedFS embed.FS

// GetFS 返回以 dist 为根的只读文件系统，使调用方无需带 "dist/" 前缀。
func GetFS() fs.FS {
	sub, err := fs.Sub(embedFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
