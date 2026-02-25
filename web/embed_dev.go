//go:build dev

package web

import (
	"net/http"
	"path/filepath"
	"runtime"
)

// staticDir returns the absolute path to the static directory relative to this
// source file. This allows `go run -tags dev` to serve files from disk so that
// CSS/JS changes take effect on browser refresh without rebuilding.
func staticDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "static")
}

// StaticHandler returns an http.Handler that serves static files from disk.
// In dev mode, files are read on every request so edits are reflected immediately.
func StaticHandler() http.Handler {
	return http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir())))
}
