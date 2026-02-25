package engine

import (
	"fmt"
	"html"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/easay/pmbot/internal/store"
)

// MarketSample captures market state at a point in time within a 5m window.
type MarketSample struct {
	Elapsed   time.Duration
	Remaining time.Duration
	BTCPrice  float64
	YesAsk    float64
	YesBid    float64
	NoAsk     float64
	NoBid     float64
}

// WindowRecord holds all data collected during one 5-minute window.
type WindowRecord struct {
	Start     time.Time
	BTCOpen   float64
	BTCClose  float64
	Change    float64
	Direction string // "Up", "Down", or "Unknown"
	Samples   []MarketSample
}

// DataCollector records market state snapshots for backtesting analysis.
type DataCollector struct {
	mu             sync.RWMutex
	startTime      time.Time
	windows        map[time.Time]*WindowRecord
	completed      []*WindowRecord
	sampleInterval time.Duration
	lastSampleTime time.Time
	store          *store.Store // optional: persist to SQLite
}

// NewDataCollector creates a new data collector.
// sampleInterval controls how often we sample within each window.
func NewDataCollector(sampleInterval time.Duration, st *store.Store) *DataCollector {
	return &DataCollector{
		startTime:      time.Now(),
		windows:        make(map[time.Time]*WindowRecord),
		sampleInterval: sampleInterval,
		store:          st,
	}
}

// Sample records a snapshot of the current market state.
// windowStart is the current 5m candle's start time.
// It throttles to one sample per sampleInterval.
func (dc *DataCollector) Sample(windowStart time.Time, btcPrice, btcOpen float64,
	yesAsk, yesBid, noAsk, noBid float64) {

	dc.mu.Lock()
	defer dc.mu.Unlock()

	now := time.Now()
	if !dc.lastSampleTime.IsZero() && now.Sub(dc.lastSampleTime) < dc.sampleInterval {
		return
	}
	dc.lastSampleTime = now

	wr, exists := dc.windows[windowStart]
	if !exists {
		wr = &WindowRecord{
			Start:     windowStart,
			BTCOpen:   btcOpen,
			Direction: "Unknown",
		}
		dc.windows[windowStart] = wr
	}

	elapsed := now.Sub(windowStart)
	remaining := 5*time.Minute - elapsed
	if remaining < 0 {
		remaining = 0
	}

	wr.BTCClose = btcPrice
	wr.Change = btcPrice - wr.BTCOpen
	wr.Samples = append(wr.Samples, MarketSample{
		Elapsed:   elapsed,
		Remaining: remaining,
		BTCPrice:  btcPrice,
		YesAsk:    yesAsk,
		YesBid:    yesBid,
		NoAsk:     noAsk,
		NoBid:     noBid,
	})
}

// ResolveWindow finalizes a completed window with its actual direction.
func (dc *DataCollector) ResolveWindow(windowStart time.Time, direction string, btcClose float64) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	wr, exists := dc.windows[windowStart]
	if !exists {
		return
	}
	wr.Direction = direction
	wr.BTCClose = btcClose
	wr.Change = btcClose - wr.BTCOpen
	dc.completed = append(dc.completed, wr)
	delete(dc.windows, windowStart)

	slog.Debug("window resolved",
		"window_end", windowStart.Add(5*time.Minute).Format("15:04:05"), "dir", direction, "change", wr.Change, "samples", len(wr.Samples))

	// Compute market signal: auxiliary win/loss indicator from ask prices.
	// If any sample had yes_ask >= 0.99, market predicted "Up";
	// if no_ask >= 0.99, market predicted "Down".
	marketSignal := ""
	for _, s := range wr.Samples {
		if s.YesAsk >= 0.99 {
			marketSignal = "Up"
			break
		}
		if s.NoAsk >= 0.99 {
			marketSignal = "Down"
			break
		}
	}

	// Persist to SQLite if store is available.
	if dc.store != nil {
		rows := make([]store.SampleRow, len(wr.Samples))
		for i, s := range wr.Samples {
			rows[i] = store.SampleRow{
				ElapsedMs:   s.Elapsed.Milliseconds(),
				RemainingMs: s.Remaining.Milliseconds(),
				BTCPrice:    s.BTCPrice,
				YesAsk:      s.YesAsk,
				YesBid:      s.YesBid,
				NoAsk:       s.NoAsk,
				NoBid:       s.NoBid,
			}
		}
		if err := dc.store.InsertWindow(wr.Start, wr.BTCOpen, wr.BTCClose, wr.Change, direction, marketSignal, rows); err != nil {
			slog.Error("db write error", "err", err)
		}
	}
}

// CorrectDirection updates the direction of a completed window in memory.
// Called when the official market_resolved direction differs from local candle.
func (dc *DataCollector) CorrectDirection(windowStart time.Time, direction string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	for _, wr := range dc.completed {
		if wr.Start.Equal(windowStart) {
			wr.Direction = direction
			return
		}
	}
}

// HasData returns true if any data has been collected.
func (dc *DataCollector) HasData() bool {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	return len(dc.completed) > 0 || len(dc.windows) > 0
}

// loadFromDBLocked replaces in-memory completed windows with all historical data
// from the database. Caller must hold dc.mu.
func (dc *DataCollector) loadFromDBLocked() {
	if dc.store == nil {
		return
	}
	rows, err := dc.store.QueryAllWindows()
	if err != nil {
		slog.Error("db read error", "err", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	// Convert store types to engine types.
	dc.completed = make([]*WindowRecord, 0, len(rows))
	for _, r := range rows {
		wr := &WindowRecord{
			Start:     r.Window.StartTime,
			BTCOpen:   r.Window.BTCOpen,
			BTCClose:  r.Window.BTCClose,
			Change:    r.Window.Change,
			Direction: r.Window.Direction,
			Samples:   make([]MarketSample, len(r.Samples)),
		}
		for i, s := range r.Samples {
			wr.Samples[i] = MarketSample{
				Elapsed:   time.Duration(s.ElapsedMs) * time.Millisecond,
				Remaining: time.Duration(s.RemainingMs) * time.Millisecond,
				BTCPrice:  s.BTCPrice,
				YesAsk:    s.YesAsk,
				YesBid:    s.YesBid,
				NoAsk:     s.NoAsk,
				NoBid:     s.NoBid,
			}
		}
		dc.completed = append(dc.completed, wr)
	}
	slog.Info("loaded windows from db", "count", len(dc.completed))
}

// Report writes the HTML report to a file.
func (dc *DataCollector) Report() {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	// Load all historical data from DB before generating report.
	dc.loadFromDBLocked()

	duration := time.Since(dc.startTime).Truncate(time.Second)
	slog.Info("collector report",
		"duration", duration, "completed", len(dc.completed), "in_progress", len(dc.windows))

	// Merge in-progress windows into a separate list.
	var inProgress []*WindowRecord
	for _, wr := range dc.windows {
		inProgress = append(inProgress, wr)
	}
	sort.Slice(inProgress, func(i, j int) bool {
		return inProgress[i].Start.Before(inProgress[j].Start)
	})

}

// timeBuckets defines the remaining-time checkpoints we care about.
var timeBuckets = []time.Duration{
	4 * time.Minute,
	3 * time.Minute,
	2 * time.Minute,
	90 * time.Second,
	60 * time.Second,
	45 * time.Second,
	30 * time.Second,
	15 * time.Second,
	5 * time.Second,
}

// sampleAtRemaining finds the sample closest to a target remaining time.
func sampleAtRemaining(samples []MarketSample, target time.Duration) (MarketSample, bool) {
	if len(samples) == 0 {
		return MarketSample{}, false
	}
	best := samples[0]
	bestDiff := absDuration(best.Remaining - target)
	for _, s := range samples[1:] {
		diff := absDuration(s.Remaining - target)
		if diff < bestDiff {
			best = s
			bestDiff = diff
		}
	}
	// Only match if within 10 seconds of target.
	if bestDiff > 10*time.Second {
		return MarketSample{}, false
	}
	return best, true
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// ServeHTTP handles HTTP requests by generating a fresh report from DB data.
func (dc *DataCollector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	dc.mu.Lock()
	dc.loadFromDBLocked()
	var inProgress []*WindowRecord
	for _, wr := range dc.windows {
		inProgress = append(inProgress, wr)
	}
	sort.Slice(inProgress, func(i, j int) bool {
		return inProgress[i].Start.Before(inProgress[j].Start)
	})
	html := dc.buildHTML(inProgress)
	dc.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

// htmlWriter is a simple string accumulator for building HTML.
type htmlWriter struct {
	buf []byte
}

func (w *htmlWriter) add(format string, args ...any) {
	w.buf = append(w.buf, fmt.Sprintf(format+"\n", args...)...)
}

func (dc *DataCollector) buildHTML(inProgress []*WindowRecord) []byte {
	duration := time.Since(dc.startTime).Truncate(time.Second)
	allWindows := append(dc.completed, inProgress...)

	w := &htmlWriter{}
	w.add(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Backtest Data Report</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#0f172a;color:#e2e8f0;padding:24px;max-width:1400px;margin:0 auto}
h1{text-align:center;font-size:24px;margin-bottom:8px;color:#f8fafc}
.sub{text-align:center;color:#64748b;margin-bottom:32px;font-size:14px}
.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:12px;margin-bottom:32px}
.card{background:#1e293b;border-radius:12px;padding:16px;text-align:center}
.card .v{font-size:26px;font-weight:700;margin-bottom:2px}
.card .l{font-size:11px;color:#94a3b8;text-transform:uppercase;letter-spacing:.05em}
.g{color:#22c55e}.r{color:#ef4444}.y{color:#eab308}.dim{color:#64748b}
.sec{margin-bottom:28px}
.sec h2{font-size:14px;color:#94a3b8;text-transform:uppercase;letter-spacing:.05em;margin-bottom:10px;padding-bottom:6px;border-bottom:1px solid #334155}
.sec p{color:#64748b;font-size:13px;margin-bottom:12px}
table{width:100%%;border-collapse:collapse;font-size:12px}
th{text-align:left;padding:6px 8px;background:#1e293b;color:#94a3b8;font-weight:600;font-size:11px;text-transform:uppercase;letter-spacing:.05em;position:sticky;top:0}
td{padding:6px 8px;border-bottom:1px solid #1e293b}
tr:hover td{background:#1e293b}
.m{font-family:'SF Mono','Fira Code',monospace;font-size:12px}
.badge{display:inline-block;padding:2px 6px;border-radius:4px;font-size:11px;font-weight:600}
.bu{background:#14532d;color:#22c55e}.bd{background:#450a0a;color:#ef4444}.bp{background:#422006;color:#eab308}
.price-cell{min-width:48px}
.highlight{background:#1e293b;font-weight:600}
@media(max-width:768px){
body{padding:12px 8px}
h1{font-size:18px}
.sub{font-size:12px;margin-bottom:20px}
.cards{grid-template-columns:repeat(3,1fr);gap:8px}
.card{padding:10px;border-radius:8px}
.card .v{font-size:18px}
.card .l{font-size:9px}
.sec{margin-bottom:20px;overflow-x:auto;-webkit-overflow-scrolling:touch}
.sec h2{font-size:12px;margin-bottom:8px}
.sec p{font-size:12px}
table{font-size:11px}
th{padding:4px 6px;font-size:10px}
td{padding:4px 6px}
.m{font-size:11px}
}
</style></head><body>`)

	// Header.
	w.add(`<h1>Backtest Data Report</h1>`)
	w.add(`<div class="sub">%s · Duration: %s · Collected from Polymarket BTC 5m Up/Down</div>`,
		time.Now().Format("2006-01-02 15:04:05"), duration)

	// Summary cards.
	upCount, downCount := 0, 0
	var totalAbsChange float64
	for _, wr := range dc.completed {
		if wr.Direction == "Up" {
			upCount++
		} else {
			downCount++
		}
		totalAbsChange += math.Abs(wr.Change)
	}
	avgChange := 0.0
	if len(dc.completed) > 0 {
		avgChange = totalAbsChange / float64(len(dc.completed))
	}

	w.add(`<div class="cards">`)
	w.add(`<div class="card"><div class="v">%s</div><div class="l">Duration</div></div>`, duration)
	w.add(`<div class="card"><div class="v">%d</div><div class="l">Completed Windows</div></div>`, len(dc.completed))
	w.add(`<div class="card"><div class="v y">%d</div><div class="l">In Progress</div></div>`, len(inProgress))
	w.add(`<div class="card"><div class="v g">%d</div><div class="l">Up Windows</div></div>`, upCount)
	w.add(`<div class="card"><div class="v r">%d</div><div class="l">Down Windows</div></div>`, downCount)
	w.add(`<div class="card"><div class="v">$%.0f</div><div class="l">Avg |Change|</div></div>`, avgChange)
	w.add(`</div>`)

	// ===== Table 1: Window Summary =====
	w.add(`<div class="sec"><h2>Window Summary</h2>`)
	w.add(`<p>Each 5-minute window with BTC price data and final direction</p>`)
	w.add(`<table><tr><th>Window</th><th>Direction</th><th>BTC Open</th><th>BTC Close</th><th>Change</th><th>Samples</th></tr>`)
	for _, wr := range allWindows {
		dirBadge := `<span class="badge bp">…</span>`
		if wr.Direction == "Up" {
			dirBadge = `<span class="badge bu">UP</span>`
		} else if wr.Direction == "Down" {
			dirBadge = `<span class="badge bd">DOWN</span>`
		}
		chgColor := "g"
		if wr.Change < 0 {
			chgColor = "r"
		}
		w.add(`<tr><td class="m">%s</td><td>%s</td><td class="m">$%.0f</td><td class="m">$%.0f</td><td class="m %s">%+.0f</td><td>%d</td></tr>`,
			wr.Start.Format("15:04:05"), dirBadge, wr.BTCOpen, wr.BTCClose, chgColor, wr.Change, len(wr.Samples))
	}
	w.add(`</table></div>`)

	// ===== Table 2: YES Ask Price at Key Remaining Times =====
	w.add(`<div class="sec"><h2>YES (Up) Ask Price by Remaining Time</h2>`)
	w.add(`<p>What price would you pay to bet "Up" at each time point? Lower = better value.</p>`)
	w.add(`<table><tr><th>Window</th><th>Dir</th><th>Chg</th>`)
	for _, tb := range timeBuckets {
		w.add(`<th>%s</th>`, fmtDuration(tb))
	}
	w.add(`</tr>`)
	for _, wr := range dc.completed {
		dirClass := "g"
		if wr.Direction == "Down" {
			dirClass = "r"
		}
		w.add(`<tr><td class="m">%s</td><td class="%s">%s</td><td class="m">%+.0f</td>`,
			wr.Start.Format("15:04:05"), dirClass, wr.Direction, wr.Change)
		for _, tb := range timeBuckets {
			if s, ok := sampleAtRemaining(wr.Samples, tb); ok && s.YesAsk > 0 {
				w.add(`<td class="m price-cell" style="color:%s">%.2f</td>`, askColor(s.YesAsk, wr.Direction == "Up"), s.YesAsk)
			} else {
				w.add(`<td class="dim">—</td>`)
			}
		}
		w.add(`</tr>`)
	}
	w.add(`</table></div>`)

	// ===== Table 3: NO Ask Price at Key Remaining Times =====
	w.add(`<div class="sec"><h2>NO (Down) Ask Price by Remaining Time</h2>`)
	w.add(`<p>What price would you pay to bet "Down" at each time point?</p>`)
	w.add(`<table><tr><th>Window</th><th>Dir</th><th>Chg</th>`)
	for _, tb := range timeBuckets {
		w.add(`<th>%s</th>`, fmtDuration(tb))
	}
	w.add(`</tr>`)
	for _, wr := range dc.completed {
		dirClass := "g"
		if wr.Direction == "Down" {
			dirClass = "r"
		}
		w.add(`<tr><td class="m">%s</td><td class="%s">%s</td><td class="m">%+.0f</td>`,
			wr.Start.Format("15:04:05"), dirClass, wr.Direction, wr.Change)
		for _, tb := range timeBuckets {
			if s, ok := sampleAtRemaining(wr.Samples, tb); ok && s.NoAsk > 0 {
				w.add(`<td class="m price-cell" style="color:%s">%.2f</td>`, askColor(s.NoAsk, wr.Direction == "Down"), s.NoAsk)
			} else {
				w.add(`<td class="dim">—</td>`)
			}
		}
		w.add(`</tr>`)
	}
	w.add(`</table></div>`)

	// ===== Table 4: Aggregated — Average Ask at Each Remaining Time by Change Range =====
	if len(dc.completed) > 0 {
		w.add(`<div class="sec"><h2>Average Winning-Side Ask by |Change| and Remaining Time</h2>`)
		w.add(`<p>Shows the typical ask price for the side that eventually wins. This is what you'd pay for a correct bet.</p>`)

		type changeRange struct {
			label string
			lo    float64
			hi    float64
		}
		changeRanges := []changeRange{
			{"$0-30", 0, 30},
			{"$30-50", 30, 50},
			{"$50-100", 50, 100},
			{"$100-200", 100, 200},
			{"$200+", 200, math.MaxFloat64},
		}

		w.add(`<table><tr><th>|Change|</th><th>Windows</th>`)
		for _, tb := range timeBuckets {
			w.add(`<th>%s</th>`, fmtDuration(tb))
		}
		w.add(`</tr>`)

		for _, cr := range changeRanges {
			// Find windows in this change range.
			var matching []*WindowRecord
			for _, wr := range dc.completed {
				ac := math.Abs(wr.Change)
				if ac >= cr.lo && ac < cr.hi {
					matching = append(matching, wr)
				}
			}
			if len(matching) == 0 {
				continue
			}

			w.add(`<tr><td class="m">%s</td><td>%d</td>`, html.EscapeString(cr.label), len(matching))

			for _, tb := range timeBuckets {
				var sum float64
				var count int
				for _, wr := range matching {
					if s, ok := sampleAtRemaining(wr.Samples, tb); ok {
						// Winning side ask: if direction is Up, use YesAsk; if Down, use NoAsk.
						ask := s.YesAsk
						if wr.Direction == "Down" {
							ask = s.NoAsk
						}
						if ask > 0 {
							sum += ask
							count++
						}
					}
				}
				if count > 0 {
					avg := sum / float64(count)
					w.add(`<td class="m" style="color:%s">%.2f</td>`, askColor(avg, true), avg)
				} else {
					w.add(`<td class="dim">—</td>`)
				}
			}
			w.add(`</tr>`)
		}
		w.add(`</table></div>`)

		// ===== Table 5: Hypothetical P&L =====
		w.add(`<div class="sec"><h2>Hypothetical P&amp;L — "Buy winning side at remaining time"</h2>`)
		w.add(`<p>If you always correctly predicted direction and bought at the shown remaining time, what would your P&amp;L be per $10 trade? (Assumes settlement at $1.00)</p>`)
		w.add(`<table><tr><th>Remaining</th><th>Trades</th><th>Avg Ask</th><th>Avg Profit/Trade</th><th>Total P&amp;L ($10/trade)</th></tr>`)

		for _, tb := range timeBuckets {
			var sum float64
			var count int
			for _, wr := range dc.completed {
				if s, ok := sampleAtRemaining(wr.Samples, tb); ok {
					ask := s.YesAsk
					if wr.Direction == "Down" {
						ask = s.NoAsk
					}
					if ask > 0 && ask < 1.0 {
						sum += ask
						count++
					}
				}
			}
			if count == 0 {
				continue
			}
			avgAsk := sum / float64(count)
			profitPerTrade := (1.0 - avgAsk) * 10
			totalPnL := profitPerTrade * float64(count)
			w.add(`<tr><td class="m">%s</td><td>%d</td><td class="m">%.4f</td>
				<td class="m" style="color:%s">$%+.2f</td>
				<td class="m" style="color:%s">$%+.2f</td></tr>`,
				fmtDuration(tb), count, avgAsk,
				pnlFmtColor(profitPerTrade), profitPerTrade,
				pnlFmtColor(totalPnL), totalPnL)
		}
		w.add(`</table></div>`)
	}

	// ===== Table 6: Arbitrage Analysis — YES + NO Ask < $1.00 =====
	if len(dc.completed) > 0 {
		// Per-window detail: YES+NO sum at each remaining time.
		w.add(`<div class="sec"><h2>Arbitrage: YES Ask + NO Ask by Remaining Time</h2>`)
		w.add(`<p>If YES + NO &lt; $1.00, buying both sides guarantees profit at settlement. <span style="color:#22c55e;font-weight:700">Green = arbitrage opportunity</span>.</p>`)
		w.add(`<table><tr><th>Window</th><th>Dir</th><th>Chg</th>`)
		for _, tb := range timeBuckets {
			w.add(`<th>%s</th>`, fmtDuration(tb))
		}
		w.add(`</tr>`)
		for _, wr := range dc.completed {
			dirClass := "g"
			if wr.Direction == "Down" {
				dirClass = "r"
			}
			w.add(`<tr><td class="m">%s</td><td class="%s">%s</td><td class="m">%+.0f</td>`,
				wr.Start.Format("15:04:05"), dirClass, wr.Direction, wr.Change)
			for _, tb := range timeBuckets {
				if s, ok := sampleAtRemaining(wr.Samples, tb); ok && s.YesAsk > 0 && s.NoAsk > 0 {
					sum := s.YesAsk + s.NoAsk
					profit := 1.0 - sum
					color := "#ef4444" // red: no arb
					if sum < 1.0 {
						color = "#22c55e" // green: arb exists
					} else if sum < 1.02 {
						color = "#eab308" // yellow: marginal
					}
					w.add(`<td class="m" style="color:%s" title="Y=%.2f N=%.2f profit=%.4f">%.3f</td>`,
						color, s.YesAsk, s.NoAsk, profit, sum)
				} else {
					w.add(`<td class="dim">—</td>`)
				}
			}
			w.add(`</tr>`)
		}
		w.add(`</table></div>`)

		// Aggregated: average YES+NO sum and arb frequency at each remaining time.
		w.add(`<div class="sec"><h2>Arbitrage Summary by Remaining Time</h2>`)
		w.add(`<p>How often does YES + NO &lt; $1.00 occur? What is the average guaranteed profit per $1 spent?</p>`)
		w.add(`<table><tr><th>Remaining</th><th>Samples</th><th>Avg Sum</th><th>Arb Count</th><th>Arb %%</th><th>Avg Arb Profit</th><th>Best Profit</th><th>Total ($1/arb)</th></tr>`)

		for _, tb := range timeBuckets {
			var totalSum float64
			var count int
			var arbCount int
			var arbProfitSum float64
			bestProfit := 0.0
			for _, wr := range dc.completed {
				s, ok := sampleAtRemaining(wr.Samples, tb)
				if !ok || s.YesAsk <= 0 || s.NoAsk <= 0 {
					continue
				}
				sum := s.YesAsk + s.NoAsk
				totalSum += sum
				count++
				if sum < 1.0 {
					profit := 1.0 - sum
					arbCount++
					arbProfitSum += profit
					if profit > bestProfit {
						bestProfit = profit
					}
				}
			}
			if count == 0 {
				continue
			}
			avgSum := totalSum / float64(count)
			arbPct := float64(arbCount) / float64(count) * 100
			avgArbProfit := 0.0
			totalArbPnL := 0.0
			if arbCount > 0 {
				avgArbProfit = arbProfitSum / float64(arbCount)
				totalArbPnL = arbProfitSum // each arb with $1 cost → profit = 1 - sum
			}
			arbColor := "#ef4444"
			if arbPct > 50 {
				arbColor = "#22c55e"
			} else if arbPct > 20 {
				arbColor = "#eab308"
			}
			w.add(`<tr><td class="m">%s</td><td>%d</td><td class="m" style="color:%s">%.4f</td>
				<td class="m" style="color:%s">%d</td><td class="m" style="color:%s">%.1f%%%%</td>
				<td class="m g">$%.4f</td><td class="m g">$%.4f</td>
				<td class="m" style="color:%s">$%.4f</td></tr>`,
				fmtDuration(tb), count, arbSumColor(avgSum), avgSum,
				arbColor, arbCount, arbColor, arbPct,
				avgArbProfit, bestProfit,
				pnlFmtColor(totalArbPnL), totalArbPnL)
		}
		w.add(`</table></div>`)
	}

	// ===== Table 7: Price Threshold Analysis — BTC Change at 99¢ / 100¢ =====
	if len(dc.completed) > 0 {
		// For each price threshold, collect the |BTC change| when the leading side
		// first reaches that ask price. This shows how much BTC must move for
		// the market to reach high confidence.
		type thresholdSample struct {
			change    float64 // BTC change from open at that moment
			remaining time.Duration
			window    time.Time
		}
		thresholds := []float64{0.90, 0.95, 0.99, 1.00}
		// Map: threshold → collected samples.
		thresholdData := make(map[float64][]thresholdSample)

		for _, wr := range dc.completed {
			if len(wr.Samples) == 0 {
				continue
			}
			for _, thresh := range thresholds {
				// Find the first sample where the leading-side ask >= threshold.
				for _, s := range wr.Samples {
					// Leading side: if BTC is up, YES is leading; if down, NO is leading.
					leadAsk := s.YesAsk
					btcChange := s.BTCPrice - wr.BTCOpen
					if btcChange < 0 {
						leadAsk = s.NoAsk
						btcChange = -btcChange // use absolute change
					}
					if leadAsk >= thresh && leadAsk > 0 {
						thresholdData[thresh] = append(thresholdData[thresh], thresholdSample{
							change:    btcChange,
							remaining: s.Remaining,
							window:    wr.Start,
						})
						break // only first hit per window per threshold
					}
				}
			}
		}

		w.add(`<div class="sec"><h2>BTC Change at Market Price Thresholds</h2>`)
		w.add(`<p>When the leading-side ask first reaches a price level, how much has BTC actually moved? Shows the "real movement" behind market confidence.</p>`)
		w.add(`<table><tr><th>Threshold</th><th>Windows</th><th>Avg |Change|</th><th>Min</th><th>Max</th><th>Median</th><th>Avg Remaining</th></tr>`)

		for _, thresh := range thresholds {
			samples := thresholdData[thresh]
			if len(samples) == 0 {
				w.add(`<tr><td class="m">$%.2f</td><td>0</td><td class="dim" colspan="5">no data</td></tr>`, thresh)
				continue
			}

			// Compute stats.
			var sumChange, sumRemaining float64
			minChange := samples[0].change
			maxChange := samples[0].change
			changes := make([]float64, len(samples))
			for i, s := range samples {
				sumChange += s.change
				sumRemaining += s.remaining.Seconds()
				changes[i] = s.change
				if s.change < minChange {
					minChange = s.change
				}
				if s.change > maxChange {
					maxChange = s.change
				}
			}
			sort.Float64s(changes)
			n := len(samples)
			avgChange := sumChange / float64(n)
			avgRemaining := time.Duration(sumRemaining/float64(n)) * time.Second
			median := changes[n/2]
			if n%2 == 0 && n >= 2 {
				median = (changes[n/2-1] + changes[n/2]) / 2
			}

			w.add(`<tr><td class="m" style="font-weight:700">$%.2f</td><td>%d / %d</td>
				<td class="m">$%.0f</td><td class="m">$%.0f</td><td class="m">$%.0f</td>
				<td class="m">$%.0f</td><td class="m">%s</td></tr>`,
				thresh, n, len(dc.completed),
				avgChange, minChange, maxChange, median,
				avgRemaining.Truncate(time.Second))
		}
		w.add(`</table></div>`)

		// Per-window detail for 99¢ and 100¢.
		for _, thresh := range []float64{0.99, 1.00} {
			samples := thresholdData[thresh]
			if len(samples) == 0 {
				continue
			}
			w.add(`<div class="sec"><h2>$%.2f Threshold — Per Window Detail</h2>`, thresh)
			w.add(`<table><tr><th>Window</th><th>|BTC Change|</th><th>Remaining</th></tr>`)
			for _, s := range samples {
				w.add(`<tr><td class="m">%s</td><td class="m">$%.0f</td><td class="m">%s</td></tr>`,
					s.window.Format("15:04:05"), s.change, s.remaining.Truncate(time.Second))
			}
			w.add(`</table></div>`)
		}
	}

	// ===== Table 8: Raw Samples (last 5 windows) =====
	showRawCount := 5
	if len(allWindows) < showRawCount {
		showRawCount = len(allWindows)
	}
	if showRawCount > 0 {
		w.add(`<div class="sec"><h2>Raw Samples (last %d windows)</h2>`, showRawCount)
		w.add(`<p>Full sample data for detailed analysis</p>`)
		startIdx := len(allWindows) - showRawCount
		for _, wr := range allWindows[startIdx:] {
			dirBadge := `<span class="badge bp">IN PROGRESS</span>`
			if wr.Direction == "Up" {
				dirBadge = `<span class="badge bu">UP</span>`
			} else if wr.Direction == "Down" {
				dirBadge = `<span class="badge bd">DOWN</span>`
			}
			w.add(`<h3 style="font-size:13px;color:#e2e8f0;margin:16px 0 8px">Window %s %s Change: %+.0f</h3>`,
				wr.Start.Format("15:04:05"), dirBadge, wr.Change)
			w.add(`<table><tr><th>Remaining</th><th>BTC</th><th>YES Ask</th><th>YES Bid</th><th>NO Ask</th><th>NO Bid</th></tr>`)
			for _, s := range wr.Samples {
				w.add(`<tr><td class="m">%s</td><td class="m">$%.0f</td><td class="m">%.4f</td><td class="m">%.4f</td><td class="m">%.4f</td><td class="m">%.4f</td></tr>`,
					fmtDuration(s.Remaining), s.BTCPrice, s.YesAsk, s.YesBid, s.NoAsk, s.NoBid)
			}
			w.add(`</table>`)
		}
		w.add(`</div>`)
	}

	w.add(`<script>setInterval(async()=>{try{const r=await fetch(location.href);const h=await r.text();const d=new DOMParser().parseFromString(h,'text/html');document.body.innerHTML=d.body.innerHTML}catch(e){}},10000)</script>`)
	w.add(`</body></html>`)
	return w.buf
}

// askColor returns green if the ask would have been profitable (winning side), red if expensive.
func askColor(ask float64, isWinningSide bool) string {
	if !isWinningSide {
		return "#64748b" // dim for losing side
	}
	if ask <= 0.70 {
		return "#22c55e" // great value
	}
	if ask <= 0.85 {
		return "#eab308" // acceptable
	}
	return "#ef4444" // expensive
}

// arbSumColor returns a color based on how close YES+NO sum is to $1.00.
func arbSumColor(sum float64) string {
	if sum < 1.0 {
		return "#22c55e" // arbitrage exists
	}
	if sum < 1.02 {
		return "#eab308" // marginal
	}
	return "#ef4444" // no opportunity
}

func fmtDuration(d time.Duration) string {
	if d >= time.Minute {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
