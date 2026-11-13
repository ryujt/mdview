package mdview

import (
	"path/filepath"
	"strings"
)

const (
	markdownExt   = ".md"
	pdfExt        = ".pdf"
	envFileName   = ".env"
	envFilePrefix = ".env."
)

// viewableExts lists file extensions shown in the tree and openable in the viewer.
// Keep it in sync with EXT_LANGUAGES/NAME_LANGUAGES in static/js/code-viewer.js.
var viewableExts = map[string]bool{
	markdownExt: true,
	pdfExt:      true,
	".txt":      true,

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

// viewableNames lists special file names openable in the viewer without an extension (compared in lowercase).
var viewableNames = map[string]bool{
	"dockerfile":     true,
	"makefile":       true,
	".gitignore":     true,
	".dockerignore":  true,
	".gitattributes": true,
	".editorconfig":  true,
}

// IsViewablePath reports whether the file at the path is shown in the tree and openable in the viewer.
// Accepts both slash-separated relative paths and OS absolute paths.
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

// IsMarkdownPath reports whether the path is a Markdown file.
func IsMarkdownPath(p string) bool {
	return strings.EqualFold(filepath.Ext(p), markdownExt)
}

// IsPDFPath reports whether the path is a PDF file.
// PDFs are binary, so their content is not sent as WS text frames; the client re-reads them by URL.
func IsPDFPath(p string) bool {
	return strings.EqualFold(filepath.Ext(p), pdfExt)
}
