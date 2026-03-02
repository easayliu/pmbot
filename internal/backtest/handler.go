package backtest

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/easay/pmbot/internal/config"
	"github.com/easay/pmbot/internal/engine"
	"github.com/easay/pmbot/internal/store"
	"github.com/easay/pmbot/web"
)

// WindowNotifier is implemented by handlers that want to be notified
// when a new window is resolved, so they can incrementally update caches.
type WindowNotifier interface {
	NotifyNewWindow()
}

// Handler serves backtest results as HTML at /backtest.
// Supports ?sweep=key=start:end:step for parameter sweeps.
type Handler struct {
	store       *store.Store
	strategyCfg config.StrategyConfig
	statsFunc   func() engine.LiveHeaderStats // optional: provides live trading stats for header

	// Result cache: avoids recomputation when data hasn't changed.
	cacheMu   sync.RWMutex
	cacheKey  string           // "windowCount|sweepSpec|split|wfSpec"
	cacheData BacktestPageData // cached page data (before applyLiveStats)

	// In-memory windows cache: avoids full DB queries on every request.
	windowsMu    sync.RWMutex
	windowsCache []store.WindowWithSamples // all windows in memory
	windowsReady bool                      // true after first load completes
}

// SetLiveStatsFunc sets the function that provides live trading stats for header display.
func (h *Handler) SetLiveStatsFunc(fn func() engine.LiveHeaderStats) {
	h.statsFunc = fn
}

// NewHandler creates a new backtest HTTP handler.
func NewHandler(st *store.Store, strategyCfg config.StrategyConfig) *Handler {
	return &Handler{store: st, strategyCfg: strategyCfg}
}

// loadWindows returns all windows from the in-memory cache.
// On first call it performs a full DB load; subsequent calls do incremental loads.
func (h *Handler) loadWindows() ([]store.WindowWithSamples, error) {
	h.windowsMu.RLock()
	if h.windowsReady {
		result := h.windowsCache
		h.windowsMu.RUnlock()
		return result, nil
	}
	h.windowsMu.RUnlock()

	// First load: full query.
	h.windowsMu.Lock()
	defer h.windowsMu.Unlock()

	// Double-check after acquiring write lock.
	if h.windowsReady {
		return h.windowsCache, nil
	}

	windows, err := h.store.QueryAllWindows()
	if err != nil {
		return nil, err
	}
	h.windowsCache = windows
	h.windowsReady = true
	slog.Info("backtest: windows cache loaded", "count", len(windows))
	return h.windowsCache, nil
}

// NotifyNewWindow is called by the engine after a new window is resolved.
// It incrementally loads new windows from DB and invalidates the result cache.
func (h *Handler) NotifyNewWindow() {
	h.windowsMu.Lock()
	defer h.windowsMu.Unlock()

	if !h.windowsReady {
		return // not yet initialized, first request will do full load
	}

	// Find the latest time in cache.
	var after time.Time
	if len(h.windowsCache) > 0 {
		after = h.windowsCache[len(h.windowsCache)-1].Window.StartTime
	}

	newWindows, err := h.store.QueryWindowsAfter(after)
	if err != nil {
		slog.Warn("backtest: incremental load failed", "err", err)
		return
	}
	if len(newWindows) == 0 {
		return
	}

	h.windowsCache = append(h.windowsCache, newWindows...)
	slog.Info("backtest: cache updated", "new", len(newWindows), "total", len(h.windowsCache))

	// Invalidate result cache since data changed.
	h.cacheMu.Lock()
	h.cacheKey = ""
	h.cacheMu.Unlock()
}

// filterWindowsByTime returns windows whose StartTime is within [from, to].
// Zero-value from/to means unbounded on that side.
func filterWindowsByTime(windows []store.WindowWithSamples, from, to time.Time) []store.WindowWithSamples {
	if from.IsZero() && to.IsZero() {
		return windows
	}
	var filtered []store.WindowWithSamples
	for _, w := range windows {
		if !from.IsZero() && w.Window.StartTime.Before(from) {
			continue
		}
		if !to.IsZero() && w.Window.StartTime.After(to) {
			continue
		}
		filtered = append(filtered, w)
	}
	return filtered
}

// parseTimeParam parses a date string in "2006-01-02" or RFC3339 format.
// Returns zero time if the string is empty or unparseable.
func parseTimeParam(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	// Try date-only format first (HTML date input).
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	// Try RFC3339.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// parseParamsOverride parses "key=val,key=val" format into a map.
func parseParamsOverride(spec string) map[string]string {
	if spec == "" {
		return nil
	}
	overrides := make(map[string]string)
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.Index(pair, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(pair[:eq])
		val := strings.TrimSpace(pair[eq+1:])
		if key != "" {
			overrides[key] = val
		}
	}
	return overrides
}

// mergeStrategyConfig clones base and applies overrides to Params.
func mergeStrategyConfig(base config.StrategyConfig, overrides map[string]string) config.StrategyConfig {
	merged := config.StrategyConfig{
		Name:   base.Name,
		Params: make(map[string]string, len(base.Params)),
	}
	for k, v := range base.Params {
		merged.Params[k] = v
	}
	for k, v := range overrides {
		merged.Params[k] = v
	}
	return merged
}

// ServeHTTP serves the SPA shell page. Data is loaded via /api/backtest/data.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := web.Templates(engine.TemplateFuncMap())

	var data struct {
		Title string
	}
	data.Title = "Backtest"

	if err := tmpl.ExecuteTemplate(w, "backtest", data); err != nil {
		slog.Error("backtest: template execution failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}


// setCache stores computed page data for future cache hits.
func (h *Handler) setCache(key string, data BacktestPageData) {
	h.cacheMu.Lock()
	h.cacheKey = key
	h.cacheData = data
	h.cacheMu.Unlock()
}
