package engine

import (
	"fmt"
	"html/template"
	"math"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// View-model structs for HTML templates
// ---------------------------------------------------------------------------

// PageData carries all data needed to render a single paper-trading report.
type PageData struct {
	ActivePage  string // "paper" or "backtest" — controls shared header nav highlight
	Title       string
	Label       string
	StartTime   string
	StartUnix   int64 // Unix timestamp for client-side time computation
	CurrentTime string
	Duration    time.Duration
	EvalCount   int
	HasResolved bool

	// Sticky header fields.
	TotalPnL  float64
	WinRate   float64
	TradeCount int

	// Summary cards.
	Wins    int
	Losses  int
	Pending int
	AvgPnL  float64

	// Risk metrics.
	Metrics MetricsData

	// Hold reasons.
	HoldReasons []HoldReasonRow
	TotalHolds  int

	// Analysis tab.
	PriceBuckets     []BucketRow
	ChangeBuckets    []BucketRow
	DirectionBuckets []BucketRow
	TimingBuckets    []BucketRow
	ProfitSim        []ProfitSimRow
	ObservedWR       float64
	BreakevenPrice   float64
	Resolved         int

	// Equity curve (pre-rendered SVG).
	EquitySVG template.HTML

	// Trade history.
	Trades []TradeRow

	// Window results (injected by MultiPaperHandler for single-price mode).
	WindowResults    []WindowResultRow
	WindowCount      int
	WindowUpCount   int
	WindowDownCount  int
	HasWindowResults bool

	// History (injected by MultiPaperHandler for single-price mode).
	HistorySessions []HistorySessionRow
	HasHistory      bool

	// Live flag (single-price mode).
	Live bool

	// Live/Paper split (always false for single-price PageData;
	// present so the header template can reference these fields).
	LiveStats  AggStats
	PaperStats AggStats
	HasLive    bool
	HasPaper   bool

	// DryRun is true when the engine runs in dry-run mode.
	// Used to visually distinguish "LIVE (DRY RUN)" from real live trading.
	DryRun bool

	// ServerTZ is the server's timezone offset string (e.g. "UTC+8", "UTC-5").
	ServerTZ string
}

// AggStats holds aggregate statistics for a group of slots (live or paper).
type AggStats struct {
	Trades      int
	Wins        int
	Losses      int
	Resolved    int
	TotalPnL    float64
	WinRate     float64
	AvgPnL      float64
	HasResolved bool
}

// LiveHeaderStats provides a snapshot of the live engine's trading stats
// for display in page headers across all pages (paper, backtest, etc.).
type LiveHeaderStats struct {
	DryRun     bool
	HasLive    bool
	HasPaper   bool
	LiveStats  AggStats
	PaperStats AggStats
	Duration   time.Duration
}

// MultiPageData carries all data needed to render the multi-price report.
type MultiPageData struct {
	ActivePage  string
	Title       string
	StartTime   string
	StartUnix   int64 // Unix timestamp for client-side time computation
	CurrentTime string
	Duration    time.Duration
	SlotCount   int

	// Aggregate sticky header (TradeCount/WinRate mirror header template fields).
	TotalPnL    float64
	AggWinRate  float64
	TotalTrades int
	TradeCount  int
	WinRate     float64
	HasResolved bool

	// Separate live vs paper aggregates.
	LiveStats    AggStats
	PaperStats   AggStats
	HasLive      bool
	HasPaper     bool

	// DryRun is true when the engine runs in dry-run mode.
	DryRun bool

	// ServerTZ is the server's timezone offset string (e.g. "UTC+8", "UTC-5").
	ServerTZ string

	// Window results.
	WindowResults    []WindowResultRow
	WindowCount      int
	WindowUpCount   int
	WindowDownCount  int
	HasWindowResults bool

	// Price comparison table.
	Slots []SlotSummaryRow

	// Per-slot detail panels.
	SlotDetails []SlotDetailData

	// History.
	HistorySessions []HistorySessionRow
	HasHistory      bool
}

// MetricsData mirrors performanceMetrics for template access.
type MetricsData struct {
	SharpeRatio    float64
	MaxDrawdown    float64
	MaxDrawdownPct float64
	ProfitFactor   float64
	Expectancy     float64
	AvgWin         float64
	AvgLoss        float64
	WinLossRatio   float64
	MaxConsecWins  int
	MaxConsecLoss  int
	RecoveryFactor float64
}

// BucketRow represents a single bucket analysis row for templates.
type BucketRow struct {
	Label    string
	Trades   int
	Wins     int
	Losses   int
	WinRate  float64
	TotalPnL float64
}

// TradeRow represents a single trade for template rendering.
type TradeRow struct {
	Number    int
	Time      string
	Side      string
	BuyPrice  float64
	SellPrice float64 // populated for early exit trades only
	Change5m  float64
	Remaining int
	Resolved  bool
	Won       bool
	FinalDir  string
	PnL       float64
	Live      bool
}

// HoldReasonRow represents a hold reason for template rendering.
type HoldReasonRow struct {
	Reason string
	Count  int
	Pct    float64
}

// ProfitSimRow represents a row in the profitability simulation table.
type ProfitSimRow struct {
	BuyPrice     float64
	WinProfit    float64
	TotalPnL     float64
	PerTrade     float64
	NeedWR       float64
	IsProfitable bool
	IsBreakeven  bool
	BarWidth     float64
}

// WindowResultRow represents a window result for template rendering.
type WindowResultRow struct {
	EndTime      string // window end time (start + 5m), aligned with Polymarket settlement
	Direction    string
	Result       string // "Win", "Lose", or "" (no trade in this window)
	MarketSignal string // "Up"/"Down" if ask >= 0.99 detected during window (auxiliary only)
	BTCOpen      float64
	BTCClose     float64
	Change       float64
	Traded       bool
	TradedSlots  []TradedSlot // entry price labels + side that traded in this window
}

// TradedSlot represents a slot that traded in a window, with its side.
type TradedSlot struct {
	Label string // entry price label, e.g. "$0.95"
	Side  string // "Up" or "Down"
	Won   bool   // true if trade side matches window direction
}

// SlotSummaryRow represents a slot in the multi-price comparison table.
type SlotSummaryRow struct {
	Label        string
	Trades       int
	Wins         int
	Losses       int
	Resolved     int
	WinRate      float64
	AvgPnL       float64
	TotalPnL     float64
	IsBest       bool
	Metrics      MetricsData
	HasResolved  bool
	LastTrade    *LastTradeInfo
	Live         bool
}

// LastTradeInfo holds data about the most recent trade for display.
type LastTradeInfo struct {
	Time     string
	Side     string
	BuyPrice float64
	Resolved bool
	Won      bool
	FinalDir string
	PnL      float64
	Ago      string
}

// SlotDetailData carries per-slot detail panel data for multi-price reports.
type SlotDetailData struct {
	Label    string
	Trades   int
	Wins     int
	Losses   int
	Resolved int
	WinRate  float64
	TotalPnL float64
	AvgPnL   float64
	HasResolved bool
	Metrics     MetricsData
	Live        bool

	LastTrade *LastTradeInfo

	// Equity curve (pre-rendered SVG).
	EquitySVG template.HTML

	// Bucket analysis.
	DirectionBuckets []BucketRow
	TimingBuckets    []BucketRow

	// Trade history.
	TradeHistory []TradeRow
}

// HistorySessionRow represents a historical session for template rendering.
type HistorySessionRow struct {
	SessionID string
	StartedAt string
	Duration  time.Duration
	Trades    int
	Wins      int
	Losses    int
	Resolved  int
	WinRate   float64
	TotalPnL  float64

	// Per-slot summary.
	SlotSummaries []HistorySlotRow

	// Trade details.
	TradeDetails []HistoryTradeRow
	HasTrades    bool
	FetchError   bool
}

// HistorySlotRow represents a per-slot summary within a historical session.
type HistorySlotRow struct {
	Slot     string
	Trades   int
	Wins     int
	Losses   int
	Resolved int
	WinRate  float64
	PnL      float64
	Live     bool
}

// HistoryTradeRow represents a trade in historical session detail.
type HistoryTradeRow struct {
	Number    int
	Time      string
	SlotLabel string
	Side      string
	BuyPrice  float64
	Change5m  float64
	Remaining int
	Resolved  bool
	Won       bool
	FinalDir  string
	PnL       float64
	Live      bool
}

// serverTZString returns the server's timezone as a UTC offset string (e.g. "UTC+8", "UTC-5").
func serverTZString() string {
	_, offset := time.Now().Zone()
	h := offset / 3600
	m := (offset % 3600) / 60
	if m < 0 {
		m = -m
	}
	if m != 0 {
		return fmt.Sprintf("UTC%+d:%02d", h, m)
	}
	if h == 0 {
		return "UTC"
	}
	return fmt.Sprintf("UTC%+d", h)
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

// toBucketRows converts unexported priceBucket slices to exported BucketRow slices,
// filtering out empty buckets.
func toBucketRows(buckets []priceBucket) []BucketRow {
	var rows []BucketRow
	for _, b := range buckets {
		n := b.wins + b.losses
		if n == 0 {
			continue
		}
		rows = append(rows, BucketRow{
			Label:    b.label,
			Trades:   n,
			Wins:     b.wins,
			Losses:   b.losses,
			WinRate:  float64(b.wins) / float64(n) * 100,
			TotalPnL: b.totalPnL,
		})
	}
	return rows
}

// toTradeRows converts PaperTrade slices to TradeRow slices.
func toTradeRows(trades []PaperTrade) []TradeRow {
	rows := make([]TradeRow, len(trades))
	for i, t := range trades {
		row := TradeRow{
			Number:    i + 1,
			Time:      t.EntryTime.Format("15:04:05"),
			Side:      t.Side,
			BuyPrice:  t.BuyPrice,
			Change5m:  t.Change5m,
			Remaining: int(t.Remaining.Seconds()),
			Resolved:  t.Resolved,
			Won:       t.Won,
			FinalDir:  t.FinalDir,
			PnL:       t.PnL,
			Live:      t.Live,
		}
		// Derive sell price for early exit trades: sellPrice = buyPrice + pnl/size.
		if t.FinalDir == "early_exit" && t.Size > 0 {
			row.SellPrice = t.BuyPrice + t.PnL/t.Size
		}
		rows[i] = row
	}
	return rows
}

// toMetricsData converts PerformanceMetrics to MetricsData.
func toMetricsData(m PerformanceMetrics) MetricsData {
	return MetricsData{
		SharpeRatio:    m.SharpeRatio,
		MaxDrawdown:    m.MaxDrawdown,
		MaxDrawdownPct: m.MaxDrawdownPct,
		ProfitFactor:   m.ProfitFactor,
		Expectancy:     m.Expectancy,
		AvgWin:         m.AvgWin,
		AvgLoss:        m.AvgLoss,
		WinLossRatio:   m.WinLossRatio,
		MaxConsecWins:  m.MaxConsecWins,
		MaxConsecLoss:  m.MaxConsecLoss,
		RecoveryFactor: m.RecoveryFactor,
	}
}

// buildProfitSimRows generates the profitability simulation rows.
func buildProfitSimRows(wins, losses, resolved int, observedWR float64) []ProfitSimRow {
	breakevenPrice := observedWR
	simPrices := []float64{0.50, 0.55, 0.60, 0.65, 0.70, 0.75, 0.80, 0.85, 0.90, 0.95}
	rows := make([]ProfitSimRow, len(simPrices))
	for i, p := range simPrices {
		winProfit := (1 - p) / p
		totalPnL := float64(wins)*winProfit - float64(losses)*1.0
		perTrade := totalPnL / float64(resolved)
		rows[i] = ProfitSimRow{
			BuyPrice:     p,
			WinProfit:    winProfit,
			TotalPnL:     totalPnL,
			PerTrade:     perTrade,
			NeedWR:       p * 100,
			IsProfitable: totalPnL > 0,
			IsBreakeven:  p == breakevenPrice,
			BarWidth:     math.Min(math.Abs(totalPnL)/10*100, 100),
		}
	}
	return rows
}

// sortedHoldReasons converts holdReasons map to sorted HoldReasonRow slice.
func sortedHoldReasons(holdReasons map[string]int, evalCount int) []HoldReasonRow {
	if len(holdReasons) == 0 {
		return nil
	}
	type kv struct {
		reason string
		count  int
	}
	var sorted []kv
	for k, v := range holdReasons {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })

	rows := make([]HoldReasonRow, len(sorted))
	for i, kv := range sorted {
		rows[i] = HoldReasonRow{
			Reason: kv.reason,
			Count:  kv.count,
			Pct:    float64(kv.count) / float64(evalCount) * 100,
		}
	}
	return rows
}

// ---------------------------------------------------------------------------
// Template FuncMap
// ---------------------------------------------------------------------------

// TemplateFuncMap returns the function map for HTML templates.
func TemplateFuncMap() template.FuncMap {
	return template.FuncMap{
		"wrColor":           wrColor,
		"pnlFmtColor":      pnlFmtColor,
		"sharpeColor":       sharpeColor,
		"profitFactorColor": profitFactorColor,
		"fmtPnL": func(v float64) string {
			return fmt.Sprintf("$%+.2f", v)
		},
		"fmtPct": func(v float64) string {
			return fmt.Sprintf("%.2f%%", v)
		},
		"fmtPrice": func(v float64) string {
			return fmt.Sprintf("%.2f", v)
		},
		"fmtPrice2": func(v float64) string {
			return fmt.Sprintf("$%.2f", v)
		},
		"fmtFloat2": func(v float64) string {
			return fmt.Sprintf("%.2f", v)
		},
		"fmtFloat4": func(v float64) string {
			return fmt.Sprintf("$%+.2f", v)
		},
		"fmtChange": func(v float64) string {
			return fmt.Sprintf("%+.2f", v)
		},
		"fmtBTC": func(v float64) string {
			return fmt.Sprintf("$%.2f", v)
		},
		"fmtPnLSigned": func(v float64) string {
			return fmt.Sprintf("$%+.2f", v)
		},
		"fmtBarWidth": func(v float64) string {
			return fmt.Sprintf("%.0f%%", v)
		},
		"minFloat": func(a, b float64) float64 {
			return math.Min(a, b)
		},
		"isNegative": func(v float64) bool {
			return v < 0
		},
		"isPositive": func(v float64) bool {
			return v >= 0
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"invWrColor": func(wr float64) template.CSS {
			return wrColor(100 - wr)
		},
		"sideColor": func(side string) template.CSS {
			if side == "Up" {
				return "var(--green)"
			}
			return "var(--red)"
		},
		// dict creates a map from alternating key-value pairs for passing
		// multiple values to sub-templates.
		"dict": func(values ...any) map[string]any {
			m := make(map[string]any, len(values)/2)
			for i := 0; i < len(values)-1; i += 2 {
				m[values[i].(string)] = values[i+1]
			}
			return m
		},
	}
}
