//go:build !dev

package web

import (
	"embed"
	"html/template"
	"sync"
)

//go:embed templates/*
var templateFS embed.FS

var (
	cachedTmpl *template.Template
	tmplOnce   sync.Once
)

// Templates returns the parsed template set. In production mode,
// templates are parsed once from embedded files and cached.
func Templates(funcMap template.FuncMap) *template.Template {
	tmplOnce.Do(func() {
		cachedTmpl = template.Must(
			template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"),
		)
	})
	return cachedTmpl
}
