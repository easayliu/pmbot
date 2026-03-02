package backtest

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/easay/pmbot/internal/engine"
)

// ---------------------------------------------------------------------------
// JSON API response types for /api/backtest/data.
// These mirror BacktestPageData but are fully JSON-serializable.
// ---------------------------------------------------------------------------

// BacktestJSON is the top-level JSON envelope for backtest API responses.
type BacktestJSON struct {
	Meta      BTMeta              `json:"meta"`
	Form      BTForm              `json:"form"`
	LivePaper engine.APILivePaper `json:"livePaper"`
	Summary   *BTSummary          `json:"summary,omitempty"`
	Split     *BTSplit            `json:"split,omitempty"`
	Results   []BTResultRow       `json:"results,omitempty"`
	WF        *BTWalkForward      `json:"walkForward,omitempty"`
	Config    *BTConfig           `json:"config,omitempty"`
}

// BTMeta holds page-level metadata.
type BTMeta struct {
	Title       string `json:"title"`
	Period      string `json:"period"`
	WindowCount int    `json:"windowCount"`
	Timestamp   string `json:"timestamp"`
	DryRun      bool   `json:"dryRun"`
}

// BTForm preserves the query parameters and config panel state.
type BTForm struct {
	SweepSpec   string         `json:"sweepSpec"`
	SplitStr    string         `json:"splitStr"`
	WFSpec      string         `json:"wfSpec"`
	FromStr     string         `json:"fromStr"`
	ToStr       string         `json:"toStr"`
	ParamsSpec  string         `json:"paramsSpec"`
	ParamGroups []BTParamGroup `json:"paramGroups,omitempty"`
}

// BTParamGroup groups related strategy parameters for the config panel.
type BTParamGroup struct {
	Name   string        `json:"name"`
	Params []BTParamItem `json:"params"`
}

// BTParamItem represents a single configurable strategy parameter.
type BTParamItem struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Value       string `json:"value"`
	Placeholder string `json:"placeholder"`
	ToggleKey   bool   `json:"toggleKey"`
}

// BTSummary holds best-of summary cards.
type BTSummary struct {
	ConfigCount    int     `json:"configCount"`
	TotalTrades    int     `json:"totalTrades"`
	BestPnL        float64 `json:"bestPnL"`
	BestWinRate    float64 `json:"bestWinRate"`
	BestSharpe     float64 `json:"bestSharpe"`
	BestExpectancy float64 `json:"bestExpectancy"`
}

// BTSplit holds train/test split validation metadata.
type BTSplit struct {
	HasSplit     bool    `json:"hasSplit"`
	SplitRatio   float64 `json:"splitRatio"`
	TrainWindows int     `json:"trainWindows"`
	TestWindows  int     `json:"testWindows"`
}

// BTResultRow represents a single row in the backtest results table.
type BTResultRow struct {
	Index         int     `json:"index"`
	Label         string  `json:"label"`
	Trades        int     `json:"trades"`
	Wins          int     `json:"wins"`
	WinRate       float64 `json:"winRate"`
	TotalPnL      float64 `json:"totalPnL"`
	Sharpe        float64 `json:"sharpe"`
	MaxDD         float64 `json:"maxDD"`
	ProfitFactor  string  `json:"profitFactor"`
	Expectancy    float64 `json:"expectancy"`
	AvgWin        float64 `json:"avgWin"`
	AvgLoss       float64 `json:"avgLoss"`
	WinLossRatio  float64 `json:"winLossRatio"`
	MaxConsecWins int     `json:"maxConsecWins"`
	MaxConsecLoss int     `json:"maxConsecLoss"`
	IsBest        bool    `json:"isBest"`
	// OOS fields (populated when split is enabled).
	OOSTrades      int     `json:"oosTrades,omitempty"`
	OOSWins        int     `json:"oosWins,omitempty"`
	OOSWinRate     float64 `json:"oosWinRate,omitempty"`
	OOSPnL         float64 `json:"oosPnL,omitempty"`
	OOSSharpe      float64 `json:"oosSharpe,omitempty"`
	OOSExpectancy  float64 `json:"oosExpectancy,omitempty"`
	PnLDegradation float64 `json:"pnlDegradation,omitempty"`
}

// BTWalkForward holds walk-forward validation results.
type BTWalkForward struct {
	TrainSize      int         `json:"trainSize"`
	TestSize       int         `json:"testSize"`
	StepSize       int         `json:"stepSize"`
	FoldCount      int         `json:"foldCount"`
	TotalOOSTrades int         `json:"totalOOSTrades"`
	TotalOOSPnL    float64     `json:"totalOOSPnL"`
	AvgOOSWinRate  float64     `json:"avgOOSWinRate"`
	AvgOOSSharpe   float64     `json:"avgOOSSharpe"`
	ParamStability float64     `json:"paramStability"`
	Folds          []BTFoldRow `json:"folds"`
}

// BTFoldRow represents a single fold in walk-forward results.
type BTFoldRow struct {
	Index       int     `json:"index"`
	TrainPeriod string  `json:"trainPeriod"`
	TestPeriod  string  `json:"testPeriod"`
	BestLabel   string  `json:"bestLabel"`
	TrainPnL    float64 `json:"trainPnL"`
	TestTrades  int     `json:"testTrades"`
	TestWins    int     `json:"testWins"`
	TestWinRate float64 `json:"testWinRate"`
	TestPnL     float64 `json:"testPnL"`
	TestSharpe  float64 `json:"testSharpe"`
	ParamStable bool    `json:"paramStable"`
}

// BTConfig holds the merged strategy config in YAML format.
type BTConfig struct {
	YAML string `json:"yaml"`
}

// toJSON converts BacktestPageData to BacktestJSON for API responses.
func (h *Handler) toJSON(data BacktestPageData, splitStr string) BacktestJSON {
	resp := BacktestJSON{
		Meta: BTMeta{
			Title:       data.Title,
			Period:      data.Period,
			WindowCount: data.WindowCount,
			Timestamp:   data.Timestamp,
			DryRun:      data.DryRun,
		},
		Form: BTForm{
			SweepSpec:  data.SweepSpec,
			SplitStr:   splitStr,
			WFSpec:     data.WFSpec,
			FromStr:    data.FromStr,
			ToStr:      data.ToStr,
			ParamsSpec: data.ParamsSpec,
		},
	}

	// Convert param groups.
	if data.ParamGroups != nil {
		groups := make([]BTParamGroup, len(data.ParamGroups))
		for i, g := range data.ParamGroups {
			items := make([]BTParamItem, len(g.Params))
			for j, p := range g.Params {
				items[j] = BTParamItem{
					Key:         p.Key,
					Label:       p.Label,
					Value:       p.Value,
					Placeholder: p.Placeholder,
					ToggleKey:   p.ToggleKey,
				}
			}
			groups[i] = BTParamGroup{Name: g.Name, Params: items}
		}
		resp.Form.ParamGroups = groups
	}

	// Populate live/paper stats from engine.
	if h.statsFunc != nil {
		stats := h.statsFunc()
		resp.LivePaper = engine.APILivePaper{
			HasLive:  stats.HasLive,
			HasPaper: stats.HasPaper,
			Live: engine.APIAgg{
				Trades:      stats.LiveStats.Trades,
				Wins:        stats.LiveStats.Wins,
				Losses:      stats.LiveStats.Losses,
				Resolved:    stats.LiveStats.Resolved,
				TotalPnL:    stats.LiveStats.TotalPnL,
				WinRate:     stats.LiveStats.WinRate,
				AvgPnL:      stats.LiveStats.AvgPnL,
				HasResolved: stats.LiveStats.HasResolved,
			},
			Paper: engine.APIAgg{
				Trades:      stats.PaperStats.Trades,
				Wins:        stats.PaperStats.Wins,
				Losses:      stats.PaperStats.Losses,
				Resolved:    stats.PaperStats.Resolved,
				TotalPnL:    stats.PaperStats.TotalPnL,
				WinRate:     stats.PaperStats.WinRate,
				AvgPnL:      stats.PaperStats.AvgPnL,
				HasResolved: stats.PaperStats.HasResolved,
			},
		}
		resp.Meta.DryRun = stats.DryRun
	}

	// Summary (only when results exist).
	if data.HasResults {
		resp.Summary = &BTSummary{
			ConfigCount:    data.ConfigCount,
			TotalTrades:    data.TotalTrades,
			BestPnL:        data.BestPnL,
			BestWinRate:    data.BestWinRate,
			BestSharpe:     data.BestSharpe,
			BestExpectancy: data.BestExpectancy,
		}
	}

	// Split metadata.
	if data.HasSplit {
		resp.Split = &BTSplit{
			HasSplit:     true,
			SplitRatio:   data.SplitRatio,
			TrainWindows: data.TrainWindows,
			TestWindows:  data.TestWindows,
		}
	}

	// Results table rows.
	if data.Results != nil {
		rows := make([]BTResultRow, len(data.Results))
		for i, r := range data.Results {
			rows[i] = BTResultRow{
				Index:          r.Index,
				Label:          r.Label,
				Trades:         r.Trades,
				Wins:           r.Wins,
				WinRate:        r.WinRate,
				TotalPnL:       r.TotalPnL,
				Sharpe:         r.Sharpe,
				MaxDD:          r.MaxDD,
				ProfitFactor:   r.ProfitFactor,
				Expectancy:     r.Expectancy,
				AvgWin:         r.AvgWin,
				AvgLoss:        r.AvgLoss,
				WinLossRatio:   r.WinLossRatio,
				MaxConsecWins:  r.MaxConsecWins,
				MaxConsecLoss:  r.MaxConsecLoss,
				IsBest:         r.IsBest,
				OOSTrades:      r.OOSTrades,
				OOSWins:        r.OOSWins,
				OOSWinRate:     r.OOSWinRate,
				OOSPnL:         r.OOSPnL,
				OOSSharpe:      r.OOSSharpe,
				OOSExpectancy:  r.OOSExpectancy,
				PnLDegradation: r.PnLDegradation,
			}
		}
		resp.Results = rows
	}

	// Walk-forward folds.
	if len(data.Folds) > 0 {
		folds := make([]BTFoldRow, len(data.Folds))
		for i, f := range data.Folds {
			folds[i] = BTFoldRow{
				Index:       f.Index,
				TrainPeriod: f.TrainPeriod,
				TestPeriod:  f.TestPeriod,
				BestLabel:   f.BestLabel,
				TrainPnL:    f.TrainPnL,
				TestTrades:  f.TestTrades,
				TestWins:    f.TestWins,
				TestWinRate: f.TestWinRate,
				TestPnL:     f.TestPnL,
				TestSharpe:  f.TestSharpe,
				ParamStable: f.ParamStable,
			}
		}
		resp.WF = &BTWalkForward{
			TrainSize:      data.TrainSize,
			TestSize:       data.TestSize,
			StepSize:       data.StepSize,
			FoldCount:      data.FoldCount,
			TotalOOSTrades: data.TotalOOSTrades,
			TotalOOSPnL:    data.TotalOOSPnL,
			AvgOOSWinRate:  data.AvgOOSWinRate,
			AvgOOSSharpe:   data.AvgOOSSharpe,
			ParamStability: data.ParamStability,
			Folds:          folds,
		}
	}

	// Config YAML.
	if data.ConfigYAML != "" {
		resp.Config = &BTConfig{YAML: data.ConfigYAML}
	}

	return resp
}

// ServeAPI handles GET /api/backtest/data and returns backtest results as JSON.
// Accepts the same query params as ServeHTTP: sweep, split, wf, from, to, params.
func (h *Handler) ServeAPI(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, `{"error":"no database"}`, http.StatusServiceUnavailable)
		return
	}

	// Load windows from in-memory cache.
	allWindows, err := h.loadWindows()
	if err != nil {
		slog.Error("backtest api: load windows failed", "err", err)
		http.Error(w, `{"error":"failed to load data"}`, http.StatusInternalServerError)
		return
	}

	// Apply from/to time range filter.
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	from := parseTimeParam(fromStr)
	to := parseTimeParam(toStr)
	if !to.IsZero() && to.Equal(to.Truncate(24*time.Hour)) {
		to = to.Add(24*time.Hour - time.Second)
	}
	windows := filterWindowsByTime(allWindows, from, to)

	// Parse query parameters.
	sweepSpec := r.URL.Query().Get("sweep")
	splitStr := r.URL.Query().Get("split")
	var splitRatio float64
	if splitStr != "" {
		splitRatio, err = strconv.ParseFloat(splitStr, 64)
		if err != nil || splitRatio <= 0 || splitRatio >= 1 {
			splitRatio = 0
		}
	}

	wfSpec := r.URL.Query().Get("wf")
	paramsSpec := r.URL.Query().Get("params")
	overrides := parseParamsOverride(paramsSpec)
	effectiveCfg := mergeStrategyConfig(h.strategyCfg, overrides)

	// Build cache key (same logic as ServeHTTP).
	key := fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s", len(windows), sweepSpec, splitStr, wfSpec, paramsSpec, fromStr, toStr)

	// Check cache.
	h.cacheMu.RLock()
	if h.cacheKey == key {
		data := h.cacheData
		h.cacheMu.RUnlock()
		resp := h.toJSON(data, splitStr)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}
	h.cacheMu.RUnlock()

	slog.Info("backtest api: computing", "windows", len(windows), "sweep", sweepSpec, "split", splitStr, "wf", wfSpec)

	var data BacktestPageData

	// Walk-forward mode.
	if wfSpec != "" && sweepSpec != "" {
		params, parseErr := ParseSweep(sweepSpec)
		if parseErr != nil {
			data = buildWalkForwardPageData(nil, windows, sweepSpec, wfSpec, h.strategyCfg, paramsSpec)
		} else {
			trainSize, testSize, stepSize, wfErr := ParseWalkForward(wfSpec)
			if wfErr != nil {
				data = buildWalkForwardPageData(nil, windows, sweepSpec, wfSpec, h.strategyCfg, paramsSpec)
			} else {
				result, runErr := WalkForward(effectiveCfg, windows, params, trainSize, testSize, stepSize)
				if runErr != nil {
					slog.Error("backtest api: walk-forward failed", "err", runErr)
					http.Error(w, `{"error":"walk-forward failed"}`, http.StatusInternalServerError)
					return
				}
				data = buildWalkForwardPageData(result, windows, sweepSpec, wfSpec, h.strategyCfg, paramsSpec)
			}
		}
	} else if splitRatio > 0 {
		// Split mode.
		if sweepSpec != "" {
			params, parseErr := ParseSweep(sweepSpec)
			if parseErr != nil {
				data = buildSplitPageData(nil, windows, sweepSpec, h.strategyCfg, paramsSpec)
			} else {
				results, sweepErr := SweepWithSplit(effectiveCfg, windows, params, splitRatio)
				if sweepErr != nil {
					slog.Error("backtest api: split sweep failed", "err", sweepErr)
					http.Error(w, `{"error":"split sweep failed"}`, http.StatusInternalServerError)
					return
				}
				data = buildSplitPageData(results, windows, sweepSpec, h.strategyCfg, paramsSpec)
			}
		} else if len(windows) > 0 {
			trainWindows, testWindows := SplitWindows(windows, splitRatio)
			trainResult, trainErr := Run(effectiveCfg, trainWindows)
			if trainErr != nil {
				slog.Error("backtest api: split train run failed", "err", trainErr)
				http.Error(w, `{"error":"split train failed"}`, http.StatusInternalServerError)
				return
			}
			var testResult RunResult
			if len(testWindows) > 0 {
				testResult, err = Run(effectiveCfg, testWindows)
				if err != nil {
					slog.Error("backtest api: split test run failed", "err", err)
					http.Error(w, `{"error":"split test failed"}`, http.StatusInternalServerError)
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
			data = buildSplitPageData(splitResults, windows, sweepSpec, h.strategyCfg, paramsSpec)
		} else {
			data = buildSplitPageData(nil, windows, sweepSpec, h.strategyCfg, paramsSpec)
		}
	} else {
		// Standard mode.
		var results []RunResult
		if sweepSpec != "" {
			params, parseErr := ParseSweep(sweepSpec)
			if parseErr != nil {
				data = buildPageData(nil, windows, sweepSpec, h.strategyCfg, paramsSpec)
			} else {
				results, err = Sweep(effectiveCfg, windows, params)
				if err != nil {
					slog.Error("backtest api: sweep failed", "err", err)
					http.Error(w, `{"error":"sweep failed"}`, http.StatusInternalServerError)
					return
				}
				data = buildPageData(results, windows, sweepSpec, h.strategyCfg, paramsSpec)
			}
		} else if len(windows) > 0 {
			results, err = RunAll(effectiveCfg, windows)
			if err != nil {
				slog.Error("backtest api: run failed", "err", err)
				http.Error(w, `{"error":"backtest failed"}`, http.StatusInternalServerError)
				return
			}
			data = buildPageData(results, windows, sweepSpec, h.strategyCfg, paramsSpec)
		} else {
			data = buildPageData(nil, windows, sweepSpec, h.strategyCfg, paramsSpec)
		}
	}

	data.FromStr = fromStr
	data.ToStr = toStr
	h.setCache(key, data)

	resp := h.toJSON(data, splitStr)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
