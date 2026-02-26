// Package backtest replays historical market windows through a strategy
// to evaluate performance metrics and supports parameter sweeps.
package backtest

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/easay/pmbot/internal/config"
	"github.com/easay/pmbot/internal/engine"
	"github.com/easay/pmbot/internal/feed"
	"github.com/easay/pmbot/internal/store"
	"github.com/easay/pmbot/internal/strategy"
)

// RunResult holds the outcome of a single backtest run.
type RunResult struct {
	Label            string
	Metrics          engine.PerformanceMetrics
	Trades           int
	Wins             int
	WinRate          float64
	TotalPnL         float64
	WindowsProcessed int
}

// SplitRunResult holds train and test metrics from a split backtest run.
type SplitRunResult struct {
	Label        string
	SplitRatio   float64
	TrainWindows int
	TestWindows  int

	// Train set metrics (in-sample).
	Train RunResult

	// Test set metrics (out-of-sample).
	Test RunResult
}

// expandEntryPrices splits a config with entry_prices (plural, comma-separated)
// into individual configs each with entry_price (singular) set.
// If entry_price is already set, returns the config as-is.
func expandEntryPrices(cfg config.StrategyConfig) []config.StrategyConfig {
	// If entry_price (singular) already set, use as-is.
	if v, ok := cfg.Params["entry_price"]; ok && v != "" {
		return []config.StrategyConfig{cfg}
	}
	// Check for entry_prices (plural, comma-separated).
	prices, ok := cfg.Params["entry_prices"]
	if !ok || prices == "" {
		return []config.StrategyConfig{cfg}
	}
	var configs []config.StrategyConfig
	for _, p := range strings.Split(prices, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		clone := config.StrategyConfig{
			Name:   cfg.Name,
			Params: make(map[string]string, len(cfg.Params)),
		}
		for k, v := range cfg.Params {
			clone.Params[k] = v
		}
		clone.Params["entry_price"] = p
		configs = append(configs, clone)
	}
	if len(configs) == 0 {
		return []config.StrategyConfig{cfg}
	}
	return configs
}

// Run replays historical windows through a strategy and returns aggregated metrics.
// If entry_prices (plural) is set, expands into individual slots and runs them
// through a shared window loop with a combined PaperTrader.
func Run(strategyCfg config.StrategyConfig, windows []store.WindowWithSamples) (RunResult, error) {
	configs := expandEntryPrices(strategyCfg)
	if len(configs) == 1 {
		return runSingle(configs[0], windows)
	}

	// Multiple entry_price slots: shared feed, per-slot strategy + bought tracking.
	type slot struct {
		strat        strategy.Strategy
		bought       bool
		entryPrice   float64
		entryElapsed time.Duration
		entrySize    float64
		entryTokenID string
	}
	slots := make([]slot, len(configs))
	for i, cfg := range configs {
		strat, err := strategy.BuildFromConfig(cfg)
		if err != nil {
			return RunResult{}, fmt.Errorf("build strategy for entry_price %s: %w", cfg.Params["entry_price"], err)
		}
		slots[i] = slot{strat: strat}
	}

	label := makeLabel(strategyCfg)
	trend := &engine.TrendTracker{}
	candles := feed.NewCandleAggregator(
		[]time.Duration{5 * time.Minute, 15 * time.Minute}, 20,
	)
	paper := engine.NewPaperTrader(label, false)
	paper.SetQuiet(true)
	processed := 0

	for _, wws := range windows {
		w := wws.Window
		samples := wws.Samples
		if len(samples) == 0 {
			continue
		}

		sort.Slice(samples, func(i, j int) bool {
			return samples[i].ElapsedMs < samples[j].ElapsedMs
		})

		// Reset per-window state.
		for i := range slots {
			slots[i].bought = false
			slots[i].entryPrice = 0
		}

		for _, s := range samples {
			simTime := w.StartTime.Add(time.Duration(s.ElapsedMs) * time.Millisecond)
			trend.Add(s.BTCPrice, simTime)
			candles.AddTick(s.BTCPrice, simTime)

			cs := buildCandleState(candles, s.ElapsedMs, s.RemainingMs)
			baseMkt := strategy.MarketInfo{
				Question: "BTC Up/Down", YesTokenID: "yes", NoTokenID: "no",
				TickSize: "0.01", NegRisk: true,
				BestAsk: s.YesAsk, BestBid: s.YesBid,
				NoBestAsk: s.NoAsk, NoBestBid: s.NoBid,
			}
			baseTrend := trend.Compute()

			for i := range slots {
				sl := &slots[i]
				mkt := baseMkt
				// Populate position and entry tracking for early exit.
				if sl.bought {
					mkt.EntryPrice = sl.entryPrice
					mkt.EntryElapsed = sl.entryElapsed
					if sl.entryTokenID == "yes" {
						mkt.YesShares = sl.entrySize
					} else {
						mkt.NoShares = sl.entrySize
					}
				}
				state := strategy.MarketState{
					BTCSpotPrice: s.BTCPrice,
					Trend:        baseTrend,
					Candles:      cs,
					Markets:      []strategy.MarketInfo{mkt},
				}
				sig := sl.strat.Evaluate(state)
				if sig.Action == strategy.Sell && sl.bought {
					side := "Up"
					if sl.entryTokenID == "no" {
						side = "Down"
					}
					paper.RecordEarlyExit(w.StartTime, side, sl.entryPrice, sig.Price, sl.entrySize)
					sl.bought = false
					sl.entryPrice = 0
				}
				if sig.Action == strategy.Buy && !sl.bought {
					side := "Up"
					if sig.TokenID == "no" {
						side = "Down"
					}
					paper.RecordBuy(w.StartTime, side, sig.Price, sig.Size,
						time.Duration(s.RemainingMs)*time.Millisecond, cs.Change5m)
					sl.bought = true
					sl.entryPrice = sig.Price
					sl.entryElapsed = cs.Elapsed5m
					sl.entrySize = sig.Size
					sl.entryTokenID = sig.TokenID
				}
			}
		}
		paper.ResolveWindow(w.StartTime, w.Direction)
		processed++
	}

	m := paper.Metrics()
	wins, losses := paper.WinLoss()
	trades := wins + losses
	winRate := 0.0
	if trades > 0 {
		winRate = float64(wins) / float64(trades) * 100
	}
	return RunResult{
		Label: label, Metrics: m, Trades: trades, Wins: wins,
		WinRate: winRate, TotalPnL: paper.TotalPnL(), WindowsProcessed: processed,
	}, nil
}

// RunAll runs each expanded entry_price independently via runSingle and returns
// per-price results sorted by PnL descending. Used by the handler for the non-sweep case.
// When multiple configs exist, runs them concurrently with a semaphore limit of 8.
func RunAll(strategyCfg config.StrategyConfig, windows []store.WindowWithSamples) ([]RunResult, error) {
	configs := expandEntryPrices(strategyCfg)

	// Single config: skip goroutine overhead.
	if len(configs) <= 1 {
		results := make([]RunResult, 0, len(configs))
		for _, cfg := range configs {
			r, err := runSingle(cfg, windows)
			if err != nil {
				return nil, err
			}
			results = append(results, r)
		}
		return results, nil
	}

	// Multiple configs: run concurrently.
	results := make([]RunResult, len(configs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var firstErr error

	for i, cfg := range configs {
		wg.Add(1)
		go func(idx int, c config.StrategyConfig) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			r, err := runSingle(c, windows)
			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
			} else {
				results[idx] = r
			}
			mu.Unlock()
		}(i, cfg)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].TotalPnL > results[j].TotalPnL
	})
	return results, nil
}

// runSingle replays historical windows through a single strategy config and returns metrics.
func runSingle(strategyCfg config.StrategyConfig, windows []store.WindowWithSamples) (RunResult, error) {
	strat, err := strategy.BuildFromConfig(strategyCfg)
	if err != nil {
		return RunResult{}, fmt.Errorf("build strategy: %w", err)
	}

	label := makeLabel(strategyCfg)
	trend := &engine.TrendTracker{}
	candles := feed.NewCandleAggregator(
		[]time.Duration{5 * time.Minute, 15 * time.Minute}, 20,
	)
	paper := engine.NewPaperTrader(label, false)
	paper.SetQuiet(true)
	processed := 0

	for _, wws := range windows {
		w := wws.Window
		samples := wws.Samples
		if len(samples) == 0 {
			continue
		}

		sort.Slice(samples, func(i, j int) bool {
			return samples[i].ElapsedMs < samples[j].ElapsedMs
		})

		bought := false
		var entryPrice float64
		var entryElapsed time.Duration
		var entrySize float64
		var entryTokenID string

		for _, s := range samples {
			simTime := w.StartTime.Add(time.Duration(s.ElapsedMs) * time.Millisecond)
			trend.Add(s.BTCPrice, simTime)
			candles.AddTick(s.BTCPrice, simTime)

			cs := buildCandleState(candles, s.ElapsedMs, s.RemainingMs)
			mkt := strategy.MarketInfo{
				Question: "BTC Up/Down", YesTokenID: "yes", NoTokenID: "no",
				TickSize: "0.01", NegRisk: true,
				BestAsk: s.YesAsk, BestBid: s.YesBid,
				NoBestAsk: s.NoAsk, NoBestBid: s.NoBid,
			}
			// Populate position and entry tracking for early exit evaluation.
			if bought {
				mkt.EntryPrice = entryPrice
				mkt.EntryElapsed = entryElapsed
				if entryTokenID == "yes" {
					mkt.YesShares = entrySize
				} else {
					mkt.NoShares = entrySize
				}
			}
			state := strategy.MarketState{
				BTCSpotPrice: s.BTCPrice,
				Trend:        trend.Compute(),
				Candles:      cs,
				Markets:      []strategy.MarketInfo{mkt},
			}
			sig := strat.Evaluate(state)
			if sig.Action == strategy.Sell && bought {
				side := "Up"
				if entryTokenID == "no" {
					side = "Down"
				}
				paper.RecordEarlyExit(w.StartTime, side, entryPrice, sig.Price, entrySize)
				bought = false
				entryPrice = 0
			}
			if sig.Action == strategy.Buy && !bought {
				side := "Up"
				if sig.TokenID == "no" {
					side = "Down"
				}
				paper.RecordBuy(w.StartTime, side, sig.Price, sig.Size,
					time.Duration(s.RemainingMs)*time.Millisecond, cs.Change5m)
				bought = true
				entryPrice = sig.Price
				entryElapsed = cs.Elapsed5m
				entrySize = sig.Size
				entryTokenID = sig.TokenID
			}
		}
		paper.ResolveWindow(w.StartTime, w.Direction)
		processed++
	}

	m := paper.Metrics()
	wins, losses := paper.WinLoss()
	trades := wins + losses
	winRate := 0.0
	if trades > 0 {
		winRate = float64(wins) / float64(trades) * 100
	}
	return RunResult{
		Label: label, Metrics: m, Trades: trades, Wins: wins,
		WinRate: winRate, TotalPnL: paper.TotalPnL(), WindowsProcessed: processed,
	}, nil
}

// buildCandleState constructs CandleState using sample-based timing (not wall clock).
func buildCandleState(candles *feed.CandleAggregator, elapsedMs, remainingMs int64) strategy.CandleState {
	cs := strategy.CandleState{
		Current5m:  string(candles.CurrentDirection(5 * time.Minute)),
		Current15m: string(candles.CurrentDirection(15 * time.Minute)),
	}
	if c, ok := candles.CurrentCandle(5 * time.Minute); ok {
		cs.Change5m = c.Close - c.Open
		cs.Partial5m = c.Partial
		cs.Elapsed5m = time.Duration(elapsedMs) * time.Millisecond
		cs.Remaining5m = time.Duration(remainingMs) * time.Millisecond
	}
	if c, ok := candles.CurrentCandle(15 * time.Minute); ok {
		cs.Change15m = c.Close - c.Open
	}
	if c, ok := candles.LastCompleted(5 * time.Minute); ok {
		cs.Last5m = string(c.Direction)
	}
	if c, ok := candles.LastCompleted(15 * time.Minute); ok {
		cs.Last15m = string(c.Direction)
	}
	completed := candles.CompletedCandles(5 * time.Minute)
	if len(completed) > 0 {
		dirs := make([]string, len(completed))
		for i, c := range completed {
			dirs[len(completed)-1-i] = string(c.Direction)
		}
		cs.RecentDirs5m = dirs
	}
	return cs
}

// paramAliases maps short label names to full config keys.
// Shared by makeLabel (key→short) and resolveParamKey (short→key).
var paramAliases = []struct{ key, short string }{
	{"fair_value_edge", "fve"},
	{"vol_sigma", "vs"},
	{"min_signal_strength", "mss"},
	{"entry_price", "ep"},
	{"trend_threshold", "tt"},
	{"min_elapsed_sec", "mes"},
	{"min_threshold", "mt"},
	{"accel_decay_vol", "adv"},
	{"trend_discount", "td"},
	{"late_window_sec", "lws"},
	{"late_window_threshold_mul", "lwtm"},
	{"mean_rev_sigma", "mrs"},
	{"streak_len", "sl"},
	{"streak_discount", "sd"},
	{"min_spread", "ms"},
}

// resolveParamKey converts a short alias (e.g. "vs") to its full config key
// (e.g. "vol_sigma"). Returns the input unchanged if no alias matches.
func resolveParamKey(key string) string {
	for _, a := range paramAliases {
		if key == a.short {
			return a.key
		}
	}
	return key
}

// spreadLabelKeys defines which params appear in labels for the spread strategy.
var spreadLabelKeys = []struct{ key, short string }{
	{"entry_price", "ep"},
	{"min_spread", "ms"},
	{"late_window_sec", "lws"},
}

// makeLabel creates a short label from key strategy params.
func makeLabel(cfg config.StrategyConfig) string {
	labelKeys := paramAliases[:4] // default: btc_updown keys
	if cfg.Name == "spread" {
		labelKeys = spreadLabelKeys
	}
	var parts []string
	for _, kv := range labelKeys {
		if v, ok := cfg.Params[kv.key]; ok && v != "" {
			parts = append(parts, kv.short+"="+v)
		}
	}
	if len(parts) == 0 {
		return "default"
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// Parameter sweep
// ---------------------------------------------------------------------------

// SweepParam defines a single parameter to sweep over.
type SweepParam struct {
	Key    string
	Values []float64
}

// ParseSweep parses a sweep specification string.
// Format: "key1=start:end:step,key2=start:end:step".
func ParseSweep(spec string) ([]SweepParam, error) {
	var params []SweepParam
	for _, p := range strings.Split(spec, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		eq := strings.Index(p, "=")
		if eq < 0 {
			return nil, fmt.Errorf("invalid sweep param %q: missing '='", p)
		}
		key := resolveParamKey(p[:eq])
		rng := strings.Split(p[eq+1:], ":")
		if len(rng) != 3 {
			return nil, fmt.Errorf("invalid range %q: need start:end:step", p[eq+1:])
		}
		start, err := strconv.ParseFloat(rng[0], 64)
		if err != nil {
			return nil, fmt.Errorf("parse sweep start %q: %w", rng[0], err)
		}
		end, err := strconv.ParseFloat(rng[1], 64)
		if err != nil {
			return nil, fmt.Errorf("parse sweep end %q: %w", rng[1], err)
		}
		step, err := strconv.ParseFloat(rng[2], 64)
		if err != nil {
			return nil, fmt.Errorf("parse sweep step %q: %w", rng[2], err)
		}
		if step <= 0 {
			return nil, fmt.Errorf("step must be positive: %v", step)
		}
		var vals []float64
		for v := start; v <= end+step*0.001; v += step {
			r := math.Round(v*10000) / 10000
			if r > end+0.0001 {
				break
			}
			vals = append(vals, r)
		}
		params = append(params, SweepParam{Key: key, Values: vals})
	}
	return params, nil
}

// Sweep runs a grid search over parameter combinations concurrently.
// Results are sorted by TotalPnL descending.
func Sweep(baseCfg config.StrategyConfig, windows []store.WindowWithSamples, params []SweepParam) ([]RunResult, error) {
	combos := cartesian(params)
	slog.Info("sweep starting", "params", len(params), "combinations", len(combos))

	results := make([]RunResult, len(combos))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var firstErr error

	for i, combo := range combos {
		wg.Add(1)
		go func(idx int, overrides map[string]string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cfg := config.StrategyConfig{
				Name:   baseCfg.Name,
				Params: make(map[string]string, len(baseCfg.Params)),
			}
			for k, v := range baseCfg.Params {
				cfg.Params[k] = v
			}
			for k, v := range overrides {
				cfg.Params[k] = v
			}
			r, err := Run(cfg, windows)
			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
			} else {
				results[idx] = r
			}
			mu.Unlock()
		}(i, combo)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].TotalPnL > results[j].TotalPnL
	})
	return results, nil
}

// ---------------------------------------------------------------------------
// Train/Test split
// ---------------------------------------------------------------------------

// SplitWindows divides windows chronologically into train and test sets.
// splitRatio is the fraction used for training (e.g. 0.7 = 70% train, 30% test).
func SplitWindows(windows []store.WindowWithSamples, splitRatio float64) (train, test []store.WindowWithSamples) {
	if splitRatio <= 0 || splitRatio >= 1 {
		return windows, nil
	}
	n := int(math.Round(float64(len(windows)) * splitRatio))
	if n <= 0 {
		return nil, windows
	}
	if n >= len(windows) {
		return windows, nil
	}
	return windows[:n], windows[n:]
}

// SweepWithSplit runs a grid search with train/test split validation.
// Results are sorted by train PnL descending.
func SweepWithSplit(baseCfg config.StrategyConfig, windows []store.WindowWithSamples, params []SweepParam, splitRatio float64) ([]SplitRunResult, error) {
	trainWindows, testWindows := SplitWindows(windows, splitRatio)
	if len(trainWindows) == 0 {
		return nil, fmt.Errorf("no training windows after split (ratio=%.2f, total=%d)", splitRatio, len(windows))
	}

	combos := cartesian(params)
	slog.Info("split sweep starting",
		"params", len(params),
		"combinations", len(combos),
		"train_windows", len(trainWindows),
		"test_windows", len(testWindows),
		"split_ratio", splitRatio,
	)

	results := make([]SplitRunResult, len(combos))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var firstErr error

	for i, combo := range combos {
		wg.Add(1)
		go func(idx int, overrides map[string]string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cfg := config.StrategyConfig{
				Name:   baseCfg.Name,
				Params: make(map[string]string, len(baseCfg.Params)),
			}
			for k, v := range baseCfg.Params {
				cfg.Params[k] = v
			}
			for k, v := range overrides {
				cfg.Params[k] = v
			}

			trainResult, err := Run(cfg, trainWindows)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}

			var testResult RunResult
			if len(testWindows) > 0 {
				testResult, err = Run(cfg, testWindows)
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
			}

			mu.Lock()
			results[idx] = SplitRunResult{
				Label:        trainResult.Label,
				SplitRatio:   splitRatio,
				TrainWindows: len(trainWindows),
				TestWindows:  len(testWindows),
				Train:        trainResult,
				Test:         testResult,
			}
			mu.Unlock()
		}(i, combo)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Train.TotalPnL > results[j].Train.TotalPnL
	})
	return results, nil
}

// cartesian produces all Cartesian product combinations of sweep parameters.
func cartesian(params []SweepParam) []map[string]string {
	if len(params) == 0 {
		return []map[string]string{{}}
	}
	first := params[0]
	rest := cartesian(params[1:])
	var out []map[string]string
	for _, v := range first.Values {
		for _, r := range rest {
			c := make(map[string]string, len(r)+1)
			for k, val := range r {
				c[k] = val
			}
			c[first.Key] = strconv.FormatFloat(v, 'f', -1, 64)
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Walk-Forward validation
// ---------------------------------------------------------------------------

// WalkForwardFold holds results for a single walk-forward fold.
type WalkForwardFold struct {
	Index      int
	TrainStart time.Time
	TrainEnd   time.Time
	TestStart  time.Time
	TestEnd    time.Time

	// Best params found on training set.
	BestLabel    string
	BestTrainPnL float64

	// Out-of-sample result using best params on test set.
	TestResult RunResult

	// Whether the best params label matches the previous fold.
	ParamStable bool
}

// WalkForwardResult holds aggregated walk-forward validation results.
type WalkForwardResult struct {
	TrainSize int
	TestSize  int
	StepSize  int
	Folds     []WalkForwardFold

	// Aggregated OOS metrics across all folds.
	TotalOOSTrades  int
	TotalOOSWins    int
	TotalOOSPnL     float64
	AvgOOSWinRate   float64
	AvgOOSSharpe    float64
	ParamStability  float64 // fraction of folds where best params match previous fold
}

// WalkForward runs rolling walk-forward validation.
// trainSize/testSize/stepSize are in number of windows.
// For each fold, it runs a sweep on the training windows, picks the best params by PnL,
// then evaluates those params on the test windows.
func WalkForward(baseCfg config.StrategyConfig, windows []store.WindowWithSamples, params []SweepParam, trainSize, testSize, stepSize int) (*WalkForwardResult, error) {
	total := len(windows)
	if trainSize+testSize > total {
		return nil, fmt.Errorf("insufficient windows: need %d (train=%d + test=%d), have %d",
			trainSize+testSize, trainSize, testSize, total)
	}
	if stepSize <= 0 {
		return nil, fmt.Errorf("step size must be positive: %d", stepSize)
	}

	var folds []WalkForwardFold
	prevBestLabel := ""

	for start := 0; start+trainSize+testSize <= total; start += stepSize {
		trainEnd := start + trainSize
		testEnd := trainEnd + testSize
		trainWin := windows[start:trainEnd]
		testWin := windows[trainEnd:testEnd]

		foldIdx := len(folds)
		slog.Info("walk-forward fold",
			"fold", foldIdx,
			"train", fmt.Sprintf("[%d:%d]", start, trainEnd),
			"test", fmt.Sprintf("[%d:%d]", trainEnd, testEnd),
		)

		// Run sweep on training set to find best params.
		sweepResults, err := Sweep(baseCfg, trainWin, params)
		if err != nil {
			return nil, fmt.Errorf("fold %d sweep: %w", foldIdx, err)
		}
		if len(sweepResults) == 0 {
			continue
		}

		// Best params = first result (sorted by PnL desc).
		best := sweepResults[0]

		// Run full sweep on test set and match by label to get OOS result for best params.
		testResults, err := Sweep(baseCfg, testWin, params)
		if err != nil {
			return nil, fmt.Errorf("fold %d test sweep: %w", foldIdx, err)
		}

		// Find matching test result by label.
		var testResult RunResult
		for _, tr := range testResults {
			if tr.Label == best.Label {
				testResult = tr
				break
			}
		}

		stable := prevBestLabel != "" && best.Label == prevBestLabel
		prevBestLabel = best.Label

		fold := WalkForwardFold{
			Index:        foldIdx,
			TrainStart:   trainWin[0].Window.StartTime,
			TrainEnd:     trainWin[len(trainWin)-1].Window.StartTime,
			TestStart:    testWin[0].Window.StartTime,
			TestEnd:      testWin[len(testWin)-1].Window.StartTime,
			BestLabel:    best.Label,
			BestTrainPnL: best.TotalPnL,
			TestResult:   testResult,
			ParamStable:  stable,
		}
		folds = append(folds, fold)
	}

	if len(folds) == 0 {
		return nil, fmt.Errorf("no folds generated (total=%d, train=%d, test=%d, step=%d)",
			total, trainSize, testSize, stepSize)
	}

	// Aggregate OOS metrics.
	result := &WalkForwardResult{
		TrainSize: trainSize,
		TestSize:  testSize,
		StepSize:  stepSize,
		Folds:     folds,
	}

	stableCount := 0
	var sharpeSum, wrSum float64
	for _, f := range folds {
		result.TotalOOSTrades += f.TestResult.Trades
		result.TotalOOSWins += f.TestResult.Wins
		result.TotalOOSPnL += f.TestResult.TotalPnL
		sharpeSum += f.TestResult.Metrics.SharpeRatio
		wrSum += f.TestResult.WinRate
		if f.ParamStable {
			stableCount++
		}
	}

	n := float64(len(folds))
	result.AvgOOSWinRate = wrSum / n
	result.AvgOOSSharpe = sharpeSum / n
	if len(folds) > 1 {
		// Stability is measured across fold transitions (n-1 transitions).
		result.ParamStability = float64(stableCount) / float64(len(folds)-1) * 100
	}

	slog.Info("walk-forward complete",
		"folds", len(folds),
		"total_oos_pnl", result.TotalOOSPnL,
		"avg_oos_wr", result.AvgOOSWinRate,
		"param_stability", result.ParamStability,
	)

	return result, nil
}

// ParseWalkForward parses a walk-forward specification string.
// Format: "trainSize:testSize:stepSize" (all integers).
func ParseWalkForward(spec string) (trainSize, testSize, stepSize int, err error) {
	parts := strings.Split(spec, ":")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid walk-forward spec %q: need train:test:step", spec)
	}
	trainSize, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse train size %q: %w", parts[0], err)
	}
	testSize, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse test size %q: %w", parts[1], err)
	}
	stepSize, err = strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse step size %q: %w", parts[2], err)
	}
	if trainSize <= 0 || testSize <= 0 || stepSize <= 0 {
		return 0, 0, 0, fmt.Errorf("all sizes must be positive: train=%d, test=%d, step=%d", trainSize, testSize, stepSize)
	}
	return trainSize, testSize, stepSize, nil
}
