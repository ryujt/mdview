package mdview

import (
	"path/filepath"
	"strings"
)

const (
	markdownExt   = ".md"
	envFileName   = ".env"
	envFilePrefix = ".env."
)

// viewableExts는 트리에 노출하고 뷰어로 열 수 있는 파일 확장자 목록이다.
// static/js/code-viewer.js 의 EXT_LANGUAGES/NAME_LANGUAGES 와 동기화를 유지해야 한다.
var viewableExts = map[string]bool{
	markdownExt: true,

	".go":     true,
	".js":     true,
	".mjs":    true,
	".cjs":    true,
	".jsx":    true,
	".ts":     true,
	".tsx":    true,
	".py":     true,
	".java":   true,
	".kt":     true,
	".kts":    true,
	".c":      true,
	".h":      true,
	".cc":     true,
	".cpp":    true,
	".hpp":    true,
	".cs":     true,
	".rs":     true,
	".rb":     true,
	".php":    true,
	".swift":  true,
	".sh":     true,
	".bash":   true,
	".zsh":    true,
	".fish":   true,
	".sql":    true,
	".lua":    true,
	".r":      true,
	".pl":     true,
	".scala":  true,
	".dart":   true,
	".vue":    true,
	".svelte": true,

	".html": true,
	".htm":  true,
	".css":  true,
	".scss": true,
	".less": true,

	".json":       true,
	".yaml":       true,
	".yml":        true,
	".toml":       true,
	".ini":        true,
	".env":        true,
	".conf":       true,
	".cfg":        true,
	".properties": true,
	".xml":        true,
	".gradle":     true,
	".mod":        true,
	".sum":        true,
	".lock":       true,
}

// viewableNames는 확장자가 없어도 뷰어로 열 수 있는 특수 파일명 목록이다(소문자 비교).
var viewableNames = map[string]bool{
	"dockerfile":     true,
	"makefile":       true,
	".gitignore":     true,
	".dockerignore":  true,
	".gitattributes": true,
	".editorconfig":  true,
}

// IsViewablePath는 해당 경로의 파일이 트리 노출/뷰어 열람 대상인지 반환한다.
// 슬래시 구분 상대 경로와 OS 절대 경로를 모두 허용한다.
func IsViewablePath(p string) bool {
	name := strings.ToLower(filepath.Base(filepath.FromSlash(p)))
	if viewableNames[name] {
		return true
	}
	if name == envFileName || strings.HasPrefix(name, envFilePrefix) {
		return true
	}
	return viewableExts[filepath.Ext(name)]
}

// IsMarkdownPath는 해당 경로가 마크다운 파일인지 반환한다.
func IsMarkdownPath(p string) bool {
	return strings.EqualFold(filepath.Ext(p), markdownExt)
}
