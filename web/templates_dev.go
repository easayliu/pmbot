//go:build dev

package web

import (
	"html/template"
	"path/filepath"
	"runtime"
)

// templatesDir returns the absolute path to the templates directory
// relative to this source file.
func templatesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "templates")
}

// Templates returns the parsed template set. In dev mode,
// templates are re-read from disk on every call so edits are
// reflected immediately on browser refresh.
func Templates(funcMap template.FuncMap) *template.Template {
	pattern := filepath.Join(templatesDir(), "*.html")
	return template.Must(
		template.New("").Funcs(funcMap).ParseGlob(pattern),
	)
}
