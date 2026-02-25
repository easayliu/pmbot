package backtest

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
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

// ServeHTTP handles GET /backtest requests.
// Supports ?sweep=key=start:end:step, ?split=0.7, and ?wf=train:test:step.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "no database", http.StatusServiceUnavailable)
		return
	}

	// Load windows from in-memory cache (full load on first request, incremental after).
	allWindows, err := h.loadWindows()
	if err != nil {
		slog.Error("backtest: load windows failed", "err", err)
		http.Error(w, "failed to load data", http.StatusInternalServerError)
		return
	}

	// Apply from/to time range filter in memory.
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	from := parseTimeParam(fromStr)
	to := parseTimeParam(toStr)
	// For date-only "to" param, extend to end of day to include all windows on that date.
	if !to.IsZero() && to.Equal(to.Truncate(24*time.Hour)) {
		to = to.Add(24*time.Hour - time.Second)
	}
	windows := filterWindowsByTime(allWindows, from, to)

	sweepSpec := r.URL.Query().Get("sweep")
	splitStr := r.URL.Query().Get("split")
	var splitRatio float64
	if splitStr != "" {
		splitRatio, err = strconv.ParseFloat(splitStr, 64)
		if err != nil || splitRatio <= 0 || splitRatio >= 1 {
			slog.Warn("backtest: invalid split ratio, ignoring", "split", splitStr)
			splitRatio = 0
		}
	}

	wfSpec := r.URL.Query().Get("wf")
	paramsSpec := r.URL.Query().Get("params")
	overrides := parseParamsOverride(paramsSpec)
	effectiveCfg := mergeStrategyConfig(h.strategyCfg, overrides)

	// Build cache key from window count and query params.
	key := fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s", len(windows), sweepSpec, splitStr, wfSpec, paramsSpec, fromStr, toStr)

	// Check cache.
	h.cacheMu.RLock()
	if h.cacheKey == key {
		data := h.cacheData
		h.cacheMu.RUnlock()
		slog.Debug("backtest: cache hit", "key", key)
		h.renderTemplate(w, data)
		return
	}
	h.cacheMu.RUnlock()

	slog.Info("backtest: computing", "windows", len(windows), "sweep", sweepSpec, "split", splitStr, "wf", wfSpec, "from", fromStr, "to", toStr, "params", paramsSpec)

	// Walk-forward mode.
	if wfSpec != "" && sweepSpec != "" {
		h.serveWalkForward(w, windows, sweepSpec, wfSpec, key, effectiveCfg, paramsSpec, fromStr, toStr)
		return
	}

	// Split mode: train/test validation.
	if splitRatio > 0 {
		h.serveSplit(w, windows, sweepSpec, splitRatio, key, effectiveCfg, paramsSpec, fromStr, toStr)
		return
	}

	// Standard mode (no split).
	var results []RunResult
	if sweepSpec != "" {
		params, err := ParseSweep(sweepSpec)
		if err != nil {
			slog.Warn("backtest: invalid sweep spec", "spec", sweepSpec, "err", err)
			data := buildPageData(nil, windows, sweepSpec, h.strategyCfg, paramsSpec)
			data.FromStr = fromStr
			data.ToStr = toStr
			h.renderTemplate(w, data)
			return
		}
		results, err = Sweep(effectiveCfg, windows, params)
		if err != nil {
			slog.Error("backtest: sweep failed", "err", err)
			http.Error(w, "sweep failed", http.StatusInternalServerError)
			return
		}
	} else if len(windows) > 0 {
		var err error
		results, err = RunAll(effectiveCfg, windows)
		if err != nil {
			slog.Error("backtest: run failed", "err", err)
			http.Error(w, "backtest failed", http.StatusInternalServerError)
			return
		}
	}

	data := buildPageData(results, windows, sweepSpec, h.strategyCfg, paramsSpec)
	data.FromStr = fromStr
	data.ToStr = toStr
	h.setCache(key, data)
	h.renderTemplate(w, data)
}

// serveSplit handles backtest requests with train/test split validation.
func (h *Handler) serveSplit(w http.ResponseWriter, windows []store.WindowWithSamples, sweepSpec string, splitRatio float64, cacheKey string, effectiveCfg config.StrategyConfig, paramsSpec, fromStr, toStr string) {
	if sweepSpec != "" {
		params, err := ParseSweep(sweepSpec)
		if err != nil {
			slog.Warn("backtest: invalid sweep spec", "spec", sweepSpec, "err", err)
			data := buildSplitPageData(nil, windows, sweepSpec, h.strategyCfg, paramsSpec)
			data.FromStr = fromStr
			data.ToStr = toStr
			h.renderTemplate(w, data)
			return
		}
		results, err := SweepWithSplit(effectiveCfg, windows, params, splitRatio)
		if err != nil {
			slog.Error("backtest: split sweep failed", "err", err)
			http.Error(w, "split sweep failed", http.StatusInternalServerError)
			return
		}
		data := buildSplitPageData(results, windows, sweepSpec, h.strategyCfg, paramsSpec)
		data.FromStr = fromStr
		data.ToStr = toStr
		h.setCache(cacheKey, data)
		h.renderTemplate(w, data)
		return
	}

	// Single run with split.
	if len(windows) == 0 {
		data := buildSplitPageData(nil, windows, sweepSpec, h.strategyCfg, paramsSpec)
		data.FromStr = fromStr
		data.ToStr = toStr
		h.renderTemplate(w, data)
		return
	}

	trainWindows, testWindows := SplitWindows(windows, splitRatio)
	trainResult, err := Run(effectiveCfg, trainWindows)
	if err != nil {
		slog.Error("backtest: split train run failed", "err", err)
		http.Error(w, "split train run failed", http.StatusInternalServerError)
		return
	}

	var testResult RunResult
	if len(testWindows) > 0 {
		testResult, err = Run(effectiveCfg, testWindows)
		if err != nil {
			slog.Error("backtest: split test run failed", "err", err)
			http.Error(w, "split test run failed", http.StatusInternalServerError)
			return
		}
	}

	splitResults := []SplitRunResult{{
		Label:        trainResult.Label,
		SplitRatio:   splitRatio,
		TrainWindows: len(trainWindows),
		TestWindows:  len(testWindows),
		Train:        trainResult,
		Test:         testResult,
	}}
	data := buildSplitPageData(splitResults, windows, sweepSpec, h.strategyCfg, paramsSpec)
	data.FromStr = fromStr
	data.ToStr = toStr
	h.setCache(cacheKey, data)
	h.renderTemplate(w, data)
}

// serveWalkForward handles backtest requests with walk-forward validation.
func (h *Handler) serveWalkForward(w http.ResponseWriter, windows []store.WindowWithSamples, sweepSpec, wfSpec, cacheKey string, effectiveCfg config.StrategyConfig, paramsSpec, fromStr, toStr string) {
	params, err := ParseSweep(sweepSpec)
	if err != nil {
		slog.Warn("backtest: invalid sweep spec for walk-forward", "spec", sweepSpec, "err", err)
		data := buildWalkForwardPageData(nil, windows, sweepSpec, wfSpec, h.strategyCfg, paramsSpec)
		data.FromStr = fromStr
		data.ToStr = toStr
		h.renderWFTemplate(w, data)
		return
	}

	trainSize, testSize, stepSize, err := ParseWalkForward(wfSpec)
	if err != nil {
		slog.Warn("backtest: invalid walk-forward spec", "spec", wfSpec, "err", err)
		data := buildWalkForwardPageData(nil, windows, sweepSpec, wfSpec, h.strategyCfg, paramsSpec)
		data.FromStr = fromStr
		data.ToStr = toStr
		h.renderWFTemplate(w, data)
		return
	}

	result, err := WalkForward(effectiveCfg, windows, params, trainSize, testSize, stepSize)
	if err != nil {
		slog.Error("backtest: walk-forward failed", "err", err)
		http.Error(w, "walk-forward failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := buildWalkForwardPageData(result, windows, sweepSpec, wfSpec, h.strategyCfg, paramsSpec)
	data.FromStr = fromStr
	data.ToStr = toStr
	h.setCache(cacheKey, data)
	h.renderWFTemplate(w, data)
}

// applyLiveStats populates the live trading header fields from the engine stats.
func (h *Handler) applyLiveStats(data *BacktestPageData) {
	if h.statsFunc == nil {
		return
	}
	stats := h.statsFunc()
	data.DryRun = stats.DryRun
	data.HasLive = stats.HasLive
	data.HasPaper = stats.HasPaper
	data.LiveStats = stats.LiveStats
	data.PaperStats = stats.PaperStats
	data.Duration = stats.Duration
}

// renderTemplate executes the backtest template with the given data.
func (h *Handler) renderTemplate(w http.ResponseWriter, data BacktestPageData) {
	h.applyLiveStats(&data)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := web.Templates(engine.TemplateFuncMap())
	if err := tmpl.ExecuteTemplate(w, "backtest", data); err != nil {
		slog.Error("backtest: template render failed", "err", err)
	}
}

// renderWFTemplate executes the walk-forward template with the given data.
func (h *Handler) renderWFTemplate(w http.ResponseWriter, data BacktestPageData) {
	h.applyLiveStats(&data)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := web.Templates(engine.TemplateFuncMap())
	if err := tmpl.ExecuteTemplate(w, "backtest", data); err != nil {
		slog.Error("backtest: wf template render failed", "err", err)
	}
}

// setCache stores computed page data for future cache hits.
func (h *Handler) setCache(key string, data BacktestPageData) {
	h.cacheMu.Lock()
	h.cacheKey = key
	h.cacheData = data
	h.cacheMu.Unlock()
}
