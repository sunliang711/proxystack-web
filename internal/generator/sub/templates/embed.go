// Package templates 内嵌订阅渲染的内置模板。
package templates

import "embed"

// SubFS 保存三种订阅格式的内置模板。
//
//go:embed *.j2
var SubFS embed.FS
