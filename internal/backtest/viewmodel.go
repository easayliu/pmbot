package backtest

import (
	"fmt"
	"strings"
	"time"

	"github.com/easay/pmbot/internal/config"
	"github.com/easay/pmbot/internal/engine"
	"github.com/easay/pmbot/internal/store"
)

// ParamGroup groups related strategy parameters for the config override panel.
type ParamGroup struct {
	Name   string
	Params []ParamItem
}

// ParamItem represents a single configurable strategy parameter.
type ParamItem struct {
	Key         string // config param key (e.g., "late_window_sec")
	Label       string // display label (e.g., "Late Window (s)")
	Value       string // current override value (empty if using base config)
	Placeholder string // base config value shown as placeholder
	ToggleKey   bool   // true if 0=off (toggle-like parameter)
}

// BacktestPageData carries all data needed to render the backtest report page.
// This unified struct is used for standard, split, and walk-forward modes.
type BacktestPageData struct {
	ActivePage  string // shared header nav highlight
	Title       string
	Period      string
	WindowCount int
	Timestamp   string
	SweepSpec   string
	WFSpec      string
	FromStr     string // from date filter value (for form preservation)
	ToStr       string // to date filter value (for form preservation)
	HasResults  bool

	// Summary cards.
	ConfigCount    int
	TotalTrades    int
	BestPnL        float64
	BestWinRate    float64
	BestSharpe     float64
	BestExpectancy float64

	// Split validation fields.
	HasSplit     bool
	SplitRatio   float64
	TrainWindows int
	TestWindows  int

	// Live trading header fields (populated from engine for shared header display).
	DryRun      bool
	HasLive     bool
	HasPaper    bool
	LiveStats   engine.AggStats
	PaperStats  engine.AggStats
	Duration    time.Duration
	HasResolved bool
	TotalPnL    float64
	WinRate     float64
	TradeCount  int

	// Results table.
	Results []BacktestResultRow

	// Strategy config override fields.
	ParamsSpec  string       // raw "key=val,key=val" override spec
	ParamGroups []ParamGroup // grouped parameters for the config panel
	ConfigYAML  string       // complete strategy config in YAML format, for copy-paste

	// Walk-forward validation fields.
	TrainSize      int
	TestSize       int
	StepSize       int
	FoldCount      int
	TotalOOSTrades int
	TotalOOSPnL    float64
	AvgOOSWinRate  float64
	AvgOOSSharpe   float64
	ParamStability float64
	Folds          []WalkForwardFoldRow
}

// BacktestResultRow represents a single row in the results table.
type BacktestResultRow struct {
	Index                        int
	Label                        string
	Trades, Wins                 int
	WinRate                      float64
	TotalPnL                     float64
	Sharpe                       float64
	MaxDD                        float64
	ProfitFactor                 string // "—" or formatted
	Expectancy                   float64
	AvgWin                       float64
	AvgLoss                      float64
	WinLossRatio                 float64
	MaxConsecWins, MaxConsecLoss int
	IsBest                       bool

	// Out-of-sample (OOS) metrics - populated when split is enabled.
	OOSTrades      int
	OOSWins        int
	OOSWinRate     float64
	OOSPnL         float64
	OOSSharpe      float64
	OOSExpectancy  float64
	PnLDegradation float64 // percentage drop from train to test PnL
}

// buildPageData converts RunResult slice and window data into a BacktestPageData.
func buildPageData(results []RunResult, windows []store.WindowWithSamples, sweepSpec string, baseCfg config.StrategyConfig, paramsSpec string) BacktestPageData {
	period := "no data"
	if len(windows) > 0 {
		first := windows[0].Window.StartTime.Format("2006-01-02")
		last := windows[len(windows)-1].Window.StartTime.Format("2006-01-02")
		if first == last {
			period = first
		} else {
			period = first + " ~ " + last
		}
	}

	data := BacktestPageData{
		ActivePage:  "backtest",
		Title:       "Backtest Results",
		Period:      period,
		WindowCount: len(windows),
		Timestamp:   time.Now().Format("2006-01-02 15:04:05"),
		SweepSpec:   sweepSpec,
		HasResults:  len(results) > 0,
		ParamsSpec:  paramsSpec,
		ParamGroups: buildParamGroups(baseCfg, paramsSpec),
		ConfigYAML:  buildConfigYAML(baseCfg, paramsSpec),
	}

	if len(results) == 0 {
		return data
	}

	best := results[0]
	totalTrades := 0
	for _, r := range results {
		totalTrades += r.Trades
	}

	data.ConfigCount = len(results)
	data.TotalTrades = totalTrades
	data.BestPnL = best.TotalPnL
	data.BestWinRate = best.WinRate
	data.BestSharpe = best.Metrics.SharpeRatio
	data.BestExpectancy = best.Metrics.Expectancy

	rows := make([]BacktestResultRow, len(results))
	for i, r := range results {
		pfStr := "—"
		if r.Metrics.ProfitFactor > 0 {
			pfStr = fmt.Sprintf("%.2f", r.Metrics.ProfitFactor)
		}
		rows[i] = BacktestResultRow{
			Index:         i + 1,
			Label:         r.Label,
			Trades:        r.Trades,
			Wins:          r.Wins,
			WinRate:       r.WinRate,
			TotalPnL:      r.TotalPnL,
			Sharpe:        r.Metrics.SharpeRatio,
			MaxDD:         r.Metrics.MaxDrawdown,
			ProfitFactor:  pfStr,
			Expectancy:    r.Metrics.Expectancy,
			AvgWin:        r.Metrics.AvgWin,
			AvgLoss:       r.Metrics.AvgLoss,
			WinLossRatio:  r.Metrics.WinLossRatio,
			MaxConsecWins: r.Metrics.MaxConsecWins,
			MaxConsecLoss: r.Metrics.MaxConsecLoss,
			IsBest:        r.TotalPnL == best.TotalPnL && r.Trades > 0,
		}
	}
	data.Results = rows

	return data
}

// buildSplitPageData converts SplitRunResult slice into BacktestPageData with OOS metrics.
func buildSplitPageData(results []SplitRunResult, windows []store.WindowWithSamples, sweepSpec string, baseCfg config.StrategyConfig, paramsSpec string) BacktestPageData {
	// Reuse period calculation same as buildPageData
	period := "no data"
	if len(windows) > 0 {
		first := windows[0].Window.StartTime.Format("2006-01-02")
		last := windows[len(windows)-1].Window.StartTime.Format("2006-01-02")
		if first == last {
			period = first
		} else {
			period = first + " ~ " + last
		}
	}

	data := BacktestPageData{
		ActivePage:  "backtest",
		Title:       "Backtest Results (Train/Test Split)",
		Period:      period,
		WindowCount: len(windows),
		Timestamp:   time.Now().Format("2006-01-02 15:04:05"),
		SweepSpec:   sweepSpec,
		HasResults:  len(results) > 0,
		HasSplit:    true,
		ParamsSpec:  paramsSpec,
		ParamGroups: buildParamGroups(baseCfg, paramsSpec),
		ConfigYAML:  buildConfigYAML(baseCfg, paramsSpec),
	}

	if len(results) == 0 {
		return data
	}

	// Use first result for split info (all results share the same split).
	data.SplitRatio = results[0].SplitRatio
	data.TrainWindows = results[0].TrainWindows
	data.TestWindows = results[0].TestWindows

	best := results[0]
	totalTrades := 0
	for _, r := range results {
		totalTrades += r.Train.Trades + r.Test.Trades
	}

	data.ConfigCount = len(results)
	data.TotalTrades = totalTrades
	data.BestPnL = best.Train.TotalPnL
	data.BestWinRate = best.Train.WinRate
	data.BestSharpe = best.Train.Metrics.SharpeRatio
	data.BestExpectancy = best.Train.Metrics.Expectancy

	rows := make([]BacktestResultRow, len(results))
	for i, r := range results {
		pfStr := "—"
		if r.Train.Metrics.ProfitFactor > 0 {
			pfStr = fmt.Sprintf("%.2f", r.Train.Metrics.ProfitFactor)
		}

		// Compute PnL degradation: how much worse is OOS vs in-sample.
		var degradation float64
		if r.Train.TotalPnL > 0 && r.Test.Trades > 0 {
			degradation = (r.Train.TotalPnL - r.Test.TotalPnL) / r.Train.TotalPnL * 100
		}

		rows[i] = BacktestResultRow{
			Index:         i + 1,
			Label:         r.Label,
			Trades:        r.Train.Trades,
			Wins:          r.Train.Wins,
			WinRate:       r.Train.WinRate,
			TotalPnL:      r.Train.TotalPnL,
			Sharpe:        r.Train.Metrics.SharpeRatio,
			MaxDD:         r.Train.Metrics.MaxDrawdown,
			ProfitFactor:  pfStr,
			Expectancy:    r.Train.Metrics.Expectancy,
			AvgWin:        r.Train.Metrics.AvgWin,
			AvgLoss:       r.Train.Metrics.AvgLoss,
			WinLossRatio:  r.Train.Metrics.WinLossRatio,
			MaxConsecWins: r.Train.Metrics.MaxConsecWins,
			MaxConsecLoss: r.Train.Metrics.MaxConsecLoss,
			IsBest:        r.Train.TotalPnL == best.Train.TotalPnL && r.Train.Trades > 0,
			// OOS metrics
			OOSTrades:      r.Test.Trades,
			OOSWins:        r.Test.Wins,
			OOSWinRate:     r.Test.WinRate,
			OOSPnL:         r.Test.TotalPnL,
			OOSSharpe:      r.Test.Metrics.SharpeRatio,
			OOSExpectancy:  r.Test.Metrics.Expectancy,
			PnLDegradation: degradation,
		}
	}
	data.Results = rows

	return data
}

// WalkForwardFoldRow represents a single fold in the walk-forward results table.
type WalkForwardFoldRow struct {
	Index        int
	TrainPeriod  string
	TestPeriod   string
	BestLabel    string
	TrainPnL     float64
	TestTrades   int
	TestWins     int
	TestWinRate  float64
	TestPnL      float64
	TestSharpe   float64
	ParamStable  bool
}

// buildWalkForwardPageData converts WalkForwardResult into template data.
func buildWalkForwardPageData(wfResult *WalkForwardResult, windows []store.WindowWithSamples, sweepSpec, wfSpec string, baseCfg config.StrategyConfig, paramsSpec string) BacktestPageData {
	period := "no data"
	if len(windows) > 0 {
		first := windows[0].Window.StartTime.Format("2006-01-02")
		last := windows[len(windows)-1].Window.StartTime.Format("2006-01-02")
		if first == last {
			period = first
		} else {
			period = first + " ~ " + last
		}
	}

	data := BacktestPageData{
		ActivePage:  "backtest",
		Title:       "Walk-Forward Validation",
		Period:      period,
		WindowCount: len(windows),
		Timestamp:   time.Now().Format("2006-01-02 15:04:05"),
		SweepSpec:   sweepSpec,
		WFSpec:      wfSpec,
		HasResults:  wfResult != nil && len(wfResult.Folds) > 0,
		ParamsSpec:  paramsSpec,
		ParamGroups: buildParamGroups(baseCfg, paramsSpec),
		ConfigYAML:  buildConfigYAML(baseCfg, paramsSpec),
	}

	if wfResult == nil || len(wfResult.Folds) == 0 {
		return data
	}

	data.TrainSize = wfResult.TrainSize
	data.TestSize = wfResult.TestSize
	data.StepSize = wfResult.StepSize
	data.FoldCount = len(wfResult.Folds)
	data.TotalOOSTrades = wfResult.TotalOOSTrades
	data.TotalOOSPnL = wfResult.TotalOOSPnL
	data.AvgOOSWinRate = wfResult.AvgOOSWinRate
	data.AvgOOSSharpe = wfResult.AvgOOSSharpe
	data.ParamStability = wfResult.ParamStability

	folds := make([]WalkForwardFoldRow, len(wfResult.Folds))
	for i, f := range wfResult.Folds {
		folds[i] = WalkForwardFoldRow{
			Index:       f.Index + 1,
			TrainPeriod: f.TrainStart.Format("01-02 15:04") + " ~ " + f.TrainEnd.Format("01-02 15:04"),
			TestPeriod:  f.TestStart.Format("01-02 15:04") + " ~ " + f.TestEnd.Format("01-02 15:04"),
			BestLabel:   f.BestLabel,
			TrainPnL:    f.BestTrainPnL,
			TestTrades:  f.TestResult.Trades,
			TestWins:    f.TestResult.Wins,
			TestWinRate: f.TestResult.WinRate,
			TestPnL:     f.TestResult.TotalPnL,
			TestSharpe:  f.TestResult.Metrics.SharpeRatio,
			ParamStable: f.ParamStable,
		}
	}
	data.Folds = folds

	return data
}

// configParamOrder defines the canonical output order for strategy params,
// matching config.yaml.example layout.
var configParamOrder = []string{
	"max_cost",
	"trend_threshold",
	"min_elapsed_sec",
	"min_elapsed_floor_sec",
	"elapsed_price_ref",
	"vol_sigma",
	"min_threshold",
	"accel_decay_vol",
	"trend_confirm",
	"trend_discount",
	"late_window_sec",
	"late_window_threshold_mul",
	"mean_rev_sigma",
	"mean_rev_max_elapsed_sec",
	"streak_len",
	"streak_discount",
	"min_signal_strength",
	"fair_value_edge",
	"entry_prices",
}

// buildConfigYAML produces a complete strategy: YAML block with base config
// merged with paramsSpec overrides. The output is ready to paste into config.yaml.
func buildConfigYAML(baseCfg config.StrategyConfig, paramsSpec string) string {
	merged := make(map[string]string, len(baseCfg.Params))
	for k, v := range baseCfg.Params {
		merged[k] = v
	}
	for k, v := range parseParamsOverride(paramsSpec) {
		merged[k] = v
	}

	var b strings.Builder
	b.WriteString("strategy:\n")
	b.WriteString(fmt.Sprintf("  name: %q\n", baseCfg.Name))
	b.WriteString("  params:\n")

	// Emit known keys in canonical order.
	emitted := make(map[string]bool, len(configParamOrder))
	for _, key := range configParamOrder {
		if val, ok := merged[key]; ok {
			b.WriteString(fmt.Sprintf("    %s: %q\n", key, val))
			emitted[key] = true
		}
	}
	// Emit any remaining keys not in the canonical list (sorted would be
	// ideal but alphabetical append is fine for unknown extras).
	for k, v := range merged {
		if !emitted[k] {
			b.WriteString(fmt.Sprintf("    %s: %q\n", k, v))
		}
	}

	return b.String()
}

// buildParamGroups constructs grouped parameter items for the config override panel.
// baseCfg provides placeholder values; paramsSpec provides current override values.
func buildParamGroups(baseCfg config.StrategyConfig, paramsSpec string) []ParamGroup {
	overrides := parseParamsOverride(paramsSpec)

	item := func(key, label string, toggle bool) ParamItem {
		return ParamItem{
			Key:         key,
			Label:       label,
			Value:       overrides[key],
			Placeholder: baseCfg.Params[key],
			ToggleKey:   toggle,
		}
	}

	return []ParamGroup{
		{
			Name: "Late Sniper",
			Params: []ParamItem{
				item("late_window_sec", "Window (s)", true),
				item("late_window_threshold_mul", "Threshold Mul", false),
			},
		},
		{
			Name: "Mean Reversion",
			Params: []ParamItem{
				item("mean_rev_sigma", "Sigma", true),
				item("mean_rev_max_elapsed_sec", "Max Elapsed (s)", false),
			},
		},
		{
			Name: "Trend Following",
			Params: []ParamItem{
				item("vol_sigma", "Vol Sigma", true),
				item("min_threshold", "Min Threshold", false),
				item("trend_threshold", "Trend Threshold", false),
				item("trend_confirm", "Confirm (1m/5m)", false),
				item("trend_discount", "Discount", false),
				item("accel_decay_vol", "Accel Decay", true),
			},
		},
		{
			Name: "Signal Filter",
			Params: []ParamItem{
				item("min_signal_strength", "Min Strength", true),
				item("fair_value_edge", "FV Edge", true),
			},
		},
		{
			Name: "Entry",
			Params: []ParamItem{
				item("entry_prices", "Entry Prices", false),
				item("max_cost", "Max Cost ($)", false),
				item("min_elapsed_sec", "Min Elapsed (s)", false),
				item("min_elapsed_floor_sec", "Elapsed Floor (s)", false),
				item("elapsed_price_ref", "Elapsed Price Ref", false),
			},
		},
		{
			Name: "Streak",
			Params: []ParamItem{
				item("streak_len", "Streak Len", true),
				item("streak_discount", "Discount", false),
			},
		},
	}
}
