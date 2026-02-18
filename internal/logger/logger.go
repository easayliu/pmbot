package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ANSI color codes for terminal output.
const (
	reset  = "\033[0m"
	dim    = "\033[2m"
	bold   = "\033[1m"
	red    = "\033[31m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
)

// Handler is a colored terminal slog handler.
type Handler struct {
	w     io.Writer
	mu    sync.Mutex
	level slog.Level
	attrs []slog.Attr
	group string
}

// Init sets up the default slog logger.
// Supported formats: "json" for structured JSON output (production),
// anything else for colored terminal output (development).
func Init(level slog.Level, format string) {
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		h = &Handler{w: os.Stderr, level: level}
	}
	slog.SetDefault(slog.New(h))
}

// Enabled reports whether the handler handles records at the given level.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle formats and writes a log record with ANSI colors.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	ts := r.Time.Format("15:04:05")

	var levelStr string
	switch {
	case r.Level >= slog.LevelError:
		levelStr = red + bold + "ERR" + reset
	case r.Level >= slog.LevelWarn:
		levelStr = yellow + "WRN" + reset
	case r.Level >= slog.LevelInfo:
		levelStr = cyan + "INF" + reset
	default:
		levelStr = dim + "DBG" + reset
	}

	// Extract caller source location for debugging.
	var source string
	if r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		source = filepath.Base(f.File) + ":" + strconv.Itoa(f.Line)
	}

	line := fmt.Sprintf("%s%s%s %s %s%s%s %s", dim, ts, reset, levelStr, dim, source, reset, r.Message)

	// Append pre-set attrs and record attrs.
	// String values containing spaces or special characters are quoted,
	// consistent with the standard slog.TextHandler behavior.
	writeAttr := func(a slog.Attr) {
		key := a.Key
		if h.group != "" {
			key = h.group + key
		}
		val := a.Value.String()
		if a.Value.Kind() == slog.KindString && needsQuoting(val) {
			val = fmt.Sprintf("%q", val)
		}
		line += fmt.Sprintf(" %s%s=%s%s", dim, key, reset, val)
	}
	for _, a := range h.attrs {
		writeAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(a)
		return true
	})

	line += "\n"

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write([]byte(line))
	return err
}

// needsQuoting reports whether a string value requires quoting in log output.
// Mirrors the standard slog.TextHandler: quote if the value contains spaces,
// quotes, control characters, or is empty.
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	return strings.ContainsAny(s, " \t\n\r\"\\=")
}

// WithAttrs returns a new handler with the given attributes pre-set.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	newAttrs = append(newAttrs, attrs...)
	return &Handler{w: h.w, level: h.level, attrs: newAttrs, group: h.group}
}

// WithGroup returns a new handler with the given group prefix.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{w: h.w, level: h.level, attrs: h.attrs, group: h.group + name + "."}
}
