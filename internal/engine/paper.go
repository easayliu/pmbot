package engine

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/easay/pmbot/internal/store"
	"github.com/easay/pmbot/web"
)


// PaperTrade represents a simulated trade in dry-run mode.
type PaperTrade struct {
	EntryTime   time.Time
	WindowStart time.Time
	Side        string  // "Up" or "Down"
	BuyPrice    float64
	Size        float64
	Remaining   time.Duration // time remaining in window at entry
	Change5m    float64       // BTC price change in window at entry ($)
	Live        bool          // true = backed by real order
	// Resolution fields, populated when the window completes.
	Resolved bool
	Won      bool
	PnL      float64
	FinalDir string // actual window direction after close
}

// PaperTrader tracks simulated trades and generates performance reports.
type PaperTrader struct {
	mu          sync.RWMutex
	label       string // display label (e.g., "$0.95") for multi-price reports
	live        bool   // true = slot places real orders
	quiet       bool   // suppress per-trade logs (used in backtest)
	startTime   time.Time
	trades      []PaperTrade
	totalPnL    float64
	wins        int
	losses      int
	holdReasons map[string]int // count of each Hold reason for diagnostics
	evalCount   int            // total evaluate() calls
}

// NewPaperTrader creates a new paper trading tracker.
// label is used in multi-price reports to identify this price level.
// live indicates whether this slot places real orders.
func NewPaperTrader(label string, live bool) *PaperTrader {
	return &PaperTrader{
		label:       label,
		live:        live,
		startTime:   time.Now(),
		holdReasons: make(map[string]int),
	}
}

// SetQuiet suppresses per-trade Info logs (used in backtest).
func (pt *PaperTrader) SetQuiet(q bool) {
	pt.quiet = q
}

// Label returns the display label for this paper trader.
func (pt *PaperTrader) Label() string {
	return pt.label
}

// RecordBuy adds a new simulated buy trade with market context.
func (pt *PaperTrader) RecordBuy(windowStart time.Time, side string, price, size float64, remaining time.Duration, change5m float64) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.trades = append(pt.trades, PaperTrade{
		EntryTime:   time.Now(),
		WindowStart: windowStart,
		Side:        side,
		BuyPrice:    price,
		Size:        size,
		Remaining:   remaining,
		Change5m:    change5m,
		Live:        pt.live,
	})
	if !pt.quiet {
		slog.Info("paper buy",
			"slot", pt.label, "side", side, "price", price,
			"size", size, "remaining", remaining.Truncate(time.Second),
			"change5m", change5m, "window_end", windowStart.Add(5*time.Minute).Format("15:04:05"))
	}
}

// RecordHold increments the counter for a Hold reason.
func (pt *PaperTrader) RecordHold(reason string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.evalCount++
	// Normalize: strip dynamic values, keep the prefix for grouping.
	key := reason
	for _, prefix := range []string{
		"waiting:",
		"waiting for end-of-window",
		"no clear signal at end-of-window",
		"already holding",
	} {
		if len(reason) >= len(prefix) && reason[:len(prefix)] == prefix {
			key = prefix
			break
		}
	}
	pt.holdReasons[key]++
}

// RecordEarlyExit records a stop-loss early exit for a position.
// The trade is marked as resolved immediately so ResolveWindow skips it.
func (pt *PaperTrader) RecordEarlyExit(windowStart time.Time, side string, entryPrice, sellPrice float64, shares float64) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pnl := (sellPrice - entryPrice) * shares

	// Find the matching unresolved trade and mark it as early-exited.
	for i := range pt.trades {
		t := &pt.trades[i]
		if t.Resolved || !t.WindowStart.Equal(windowStart) || t.Side != side {
			continue
		}
		t.Resolved = true
		t.FinalDir = "early_exit"
		t.Won = pnl > 0
		t.PnL = pnl
		pt.totalPnL += pnl
		if t.Won {
			pt.wins++
		} else {
			pt.losses++
		}

		if !pt.quiet {
			slog.Info("paper early exit",
				"slot", pt.label, "side", side,
				"entry", entryPrice, "sell", sellPrice,
				"pnl", pnl, "cumulative", pt.totalPnL)
		}
		return
	}

	// No matching trade found — record as a standalone resolved trade.
	won := pnl > 0
	pt.trades = append(pt.trades, PaperTrade{
		EntryTime:   time.Now(),
		WindowStart: windowStart,
		Side:        side,
		BuyPrice:    entryPrice,
		Size:        shares,
		Resolved:    true,
		Won:         won,
		PnL:         pnl,
		FinalDir:    "early_exit",
	})
	pt.totalPnL += pnl
	if won {
		pt.wins++
	} else {
		pt.losses++
	}

	if !pt.quiet {
		slog.Info("paper early exit",
			"slot", pt.label, "side", side,
			"entry", entryPrice, "sell", sellPrice,
			"pnl", pnl, "cumulative", pt.totalPnL)
	}
}

// ResolveWindow settles all pending trades belonging to the given window.
func (pt *PaperTrader) ResolveWindow(windowStart time.Time, direction string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	for i := range pt.trades {
		t := &pt.trades[i]
		if t.Resolved || !t.WindowStart.Equal(windowStart) {
			continue
		}
		t.Resolved = true
		t.FinalDir = direction
		t.Won = t.Side == direction

		if t.Won {
			t.PnL = (1.0 - t.BuyPrice) * t.Size
			pt.wins++
		} else {
			t.PnL = -t.BuyPrice * t.Size
			pt.losses++
		}
		pt.totalPnL += t.PnL

		if !pt.quiet {
			slog.Info("paper resolved",
				"slot", pt.label, "side", t.Side, "direction", direction,
				"result", wonStr(t.Won), "pnl", t.PnL, "cumulative", pt.totalPnL)
		}
	}
}

// CorrectResolution re-resolves trades for a window when the official
// market_resolved direction differs from the locally-computed direction.
// Returns true if any trade was actually corrected.
func (pt *PaperTrader) CorrectResolution(windowStart time.Time, newDir string) bool {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	corrected := false
	for i := range pt.trades {
		t := &pt.trades[i]
		if !t.Resolved || !t.WindowStart.Equal(windowStart) || t.FinalDir == newDir {
			continue
		}
		// Undo previous resolution stats.
		pt.totalPnL -= t.PnL
		if t.Won {
			pt.wins--
		} else {
			pt.losses--
		}
		// Apply official direction.
		oldDir := t.FinalDir
		t.FinalDir = newDir
		t.Won = t.Side == newDir
		if t.Won {
			t.PnL = (1.0 - t.BuyPrice) * t.Size
			pt.wins++
		} else {
			t.PnL = -t.BuyPrice * t.Size
			pt.losses++
		}
		pt.totalPnL += t.PnL
		corrected = true

		slog.Warn("paper corrected",
			"slot", pt.label, "side", t.Side,
			"old_dir", oldDir, "new_dir", newDir,
			"result", wonStr(t.Won), "pnl", t.PnL, "cumulative", pt.totalPnL)
	}
	return corrected
}

// Metrics returns a snapshot of the current performance metrics.
func (pt *PaperTrader) Metrics() PerformanceMetrics {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.computeMetrics()
}

// Trades returns a copy of all trades for external analysis (e.g., backtesting).
func (pt *PaperTrader) Trades() []PaperTrade {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	out := make([]PaperTrade, len(pt.trades))
	copy(out, pt.trades)
	return out
}

// TotalPnL returns the cumulative P&L.
func (pt *PaperTrader) TotalPnL() float64 {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.totalPnL
}

// WinLoss returns (wins, losses) counts.
func (pt *PaperTrader) WinLoss() (int, int) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.wins, pt.losses
}

// HasTrades returns true if any trades have been recorded.
func (pt *PaperTrader) HasTrades() bool {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return len(pt.trades) > 0
}

// lastTrade returns a pointer to the most recent trade, or nil if none exist.
// The caller must hold pt.mu (at least RLock).
func (pt *PaperTrader) lastTrade() *PaperTrade {
	if len(pt.trades) == 0 {
		return nil
	}
	return &pt.trades[len(pt.trades)-1]
}

// priceBucket groups resolved trades by a numeric range for analysis.
type priceBucket struct {
	label    string
	lo, hi   float64
	wins     int
	losses   int
	totalPnL float64
}

// Report logs a brief summary to terminal and writes the full HTML report to file.
func (pt *PaperTrader) Report() {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	total := len(pt.trades)
	resolved := pt.wins + pt.losses
	pending := total - resolved
	duration := time.Since(pt.startTime).Truncate(time.Second)

	if resolved > 0 {
		winRate := float64(pt.wins) / float64(resolved) * 100
		m := pt.computeMetrics()
		slog.Info("paper summary",
			"slot", pt.label, "duration", duration,
			"trades", total, "wins", pt.wins, "losses", pt.losses, "pending", pending,
			"win_rate", fmt.Sprintf("%.1f%%", winRate), "pnl", pt.totalPnL,
			"sharpe", fmt.Sprintf("%.2f", m.SharpeRatio),
			"max_dd", fmt.Sprintf("%.2f", m.MaxDrawdown),
			"profit_factor", fmt.Sprintf("%.2f", m.ProfitFactor),
			"expectancy", fmt.Sprintf("%.4f", m.Expectancy))
	} else {
		slog.Info("paper summary",
			"slot", pt.label, "duration", duration,
			"trades", total, "evals", pt.evalCount, "status", "no trades triggered")
	}

}

// ServeHTTP handles HTTP requests by generating a fresh paper trading report.
func (pt *PaperTrader) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pt.mu.RLock()
	data := pt.buildPageData()
	pt.mu.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	funcMap := TemplateFuncMap()
	tmpl := web.Templates(funcMap)
	if err := tmpl.ExecuteTemplate(w, "paper", data); err != nil {
		slog.Error("template execution failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ---------------------------------------------------------------------------
// Data preparation (replaces HTML building)
// ---------------------------------------------------------------------------

// buildPageData prepares all view data for a single paper-trading report.
// The caller must hold pt.mu (at least RLock).
func (pt *PaperTrader) buildPageData() PageData {
	resolved := pt.wins + pt.losses
	pending := len(pt.trades) - resolved
	duration := time.Since(pt.startTime).Truncate(time.Second)
	winRate := 0.0
	avgPnL := 0.0
	if resolved > 0 {
		winRate = float64(pt.wins) / float64(resolved) * 100
		avgPnL = pt.totalPnL / float64(resolved)
	}
	metrics := pt.computeMetrics()

	title := "Paper Trading Report"
	if pt.label != "" {
		title = fmt.Sprintf("Paper Trading — %s", pt.label)
	}

	// Compute total holds for hold reasons.
	totalHolds := 0
	for _, v := range pt.holdReasons {
		totalHolds += v
	}

	data := PageData{
		ActivePage:  "paper",
		Title:       title,
		Label:       pt.label,
		Live:        pt.live,
		StartTime:   pt.startTime.Format("2006-01-02 15:04:05"),
		StartUnix:   pt.startTime.Unix(),
		CurrentTime: time.Now().Format("2006-01-02 15:04:05"),
		Duration:    duration,
		EvalCount:   pt.evalCount,
		HasResolved: resolved > 0,
		ServerTZ:    serverTZString(),
		TotalPnL:    pt.totalPnL,
		WinRate:     winRate,
		TradeCount:  len(pt.trades),
		Wins:        pt.wins,
		Losses:      pt.losses,
		Pending:     pending,
		AvgPnL:      avgPnL,
		Metrics:     toMetricsData(metrics),
		HoldReasons: sortedHoldReasons(pt.holdReasons, pt.evalCount),
		TotalHolds:  totalHolds,
		Trades:      toTradeRows(pt.trades),
		EquitySVG:   renderEquitySVG(metrics),
		Resolved:    resolved,
	}

	if resolved > 0 {
		data.PriceBuckets = toBucketRows(pt.buildPriceBuckets())
		data.ChangeBuckets = toBucketRows(pt.buildChangeBuckets())
		data.DirectionBuckets = toBucketRows(pt.buildDirectionBuckets())
		data.TimingBuckets = toBucketRows(pt.buildRemainingBuckets())
		observedWR := float64(pt.wins) / float64(resolved)
		data.ObservedWR = observedWR * 100
		data.BreakevenPrice = observedWR
		data.ProfitSim = buildProfitSimRows(pt.wins, pt.losses, resolved, observedWR)
	}

	// Populate live/paper split so the header template shows the correct badge.
	agg := AggStats{
		Trades:      len(pt.trades),
		Wins:        pt.wins,
		Losses:      pt.losses,
		Resolved:    resolved,
		TotalPnL:    pt.totalPnL,
		WinRate:     winRate,
		AvgPnL:      avgPnL,
		HasResolved: resolved > 0,
	}
	if pt.live {
		data.LiveStats = agg
		data.HasLive = true
	} else {
		data.PaperStats = agg
		data.HasPaper = true
	}

	return data
}

// toLastTradeInfo converts a PaperTrade pointer to LastTradeInfo.
func toLastTradeInfo(lt *PaperTrade, longFormat bool) *LastTradeInfo {
	if lt == nil {
		return nil
	}
	timeFmt := "15:04"
	if longFormat {
		timeFmt = "15:04:05"
	}
	info := &LastTradeInfo{
		Time:     lt.EntryTime.Format(timeFmt),
		Side:     lt.Side,
		BuyPrice: lt.BuyPrice,
		Resolved: lt.Resolved,
		Won:      lt.Won,
		FinalDir: lt.FinalDir,
		PnL:      lt.PnL,
		Ago:      time.Since(lt.EntryTime).Truncate(time.Second).String(),
	}
	return info
}

// ---------------------------------------------------------------------------
// SVG rendering (pre-rendered as template.HTML)
// ---------------------------------------------------------------------------

// renderEquitySVG renders the full equity curve SVG for single-price reports.
func renderEquitySVG(metrics PerformanceMetrics) template.HTML {
	ep := metrics.EquityPoints
	pp := metrics.PeakPoints
	n := len(ep)
	if n < 2 {
		return ""
	}

	var buf bytes.Buffer
	w := func(format string, args ...any) {
		fmt.Fprintf(&buf, format+"\n", args...)
	}

	w(`<div class="mb-7"><h2 class="sec-title">Equity Curve &amp; Drawdown</h2>`)

	const chartW, chartH = 800, 240
	const padL, padR, padT, padB = 60, 20, 10, 30

	plotW := float64(chartW - padL - padR)
	plotH := float64(chartH - padT - padB)

	// Y range: include 0 and all equity values.
	minY, maxY := 0.0, 0.0
	for _, v := range ep {
		if v < minY {
			minY = v
		}
		if v > maxY {
			maxY = v
		}
	}
	for _, v := range pp {
		if v > maxY {
			maxY = v
		}
	}
	spread := maxY - minY
	if spread == 0 {
		spread = 1
	}
	minY -= spread * 0.1
	maxY += spread * 0.1
	yRange := maxY - minY

	toX := func(i int) float64 {
		return float64(padL) + float64(i)/float64(n-1)*plotW
	}
	toY := func(v float64) float64 {
		return float64(padT) + (1-(v-minY)/yRange)*plotH
	}

	w(`<svg viewBox="0 0 %d %d" class="w-full h-auto rounded-lg border border-slate-800/50" style="max-width:%dpx;background:var(--bg-surface)">`,
		chartW, chartH, chartW)

	// Gradient definition for area fill under equity line.
	lineVar := "var(--green)"
	if ep[n-1] < 0 {
		lineVar = "var(--red)"
	}
	w(`<defs><linearGradient id="eqGrad" x1="0" y1="0" x2="0" y2="1">`)
	w(`<stop offset="0%%" style="stop-color:%s;stop-opacity:0.3"/>`, lineVar)
	w(`<stop offset="100%%" style="stop-color:%s;stop-opacity:0.02"/>`, lineVar)
	w(`</linearGradient></defs>`)

	// Horizontal grid lines.
	gridSteps := 4
	for i := 1; i < gridSteps; i++ {
		gridY := minY + yRange*float64(i)/float64(gridSteps)
		py := toY(gridY)
		w(`<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" style="stroke:var(--chart-grid)" stroke-width="0.5"/>`,
			padL, py, chartW-padR, py)
		w(`<text x="%d" y="%.1f" style="fill:var(--text-dim)" font-size="9" text-anchor="end" dominant-baseline="middle">$%+.0f</text>`,
			padL-6, py, gridY)
	}

	// Zero line.
	zeroY := toY(0)
	w(`<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" style="stroke:var(--chart-grid-zero)" stroke-width="1" stroke-dasharray="4,4"/>`,
		padL, zeroY, chartW-padR, zeroY)
	w(`<text x="%d" y="%.1f" style="fill:var(--text-dim)" font-size="10" text-anchor="end" dominant-baseline="middle">$0</text>`,
		padL-6, zeroY)

	// Drawdown fill: area between peak line and equity line.
	var ddBuf strings.Builder
	fmt.Fprintf(&ddBuf, "M%.1f,%.1f", toX(0), toY(pp[0]))
	for i := 1; i < n; i++ {
		fmt.Fprintf(&ddBuf, " L%.1f,%.1f", toX(i), toY(pp[i]))
	}
	for i := n - 1; i >= 0; i-- {
		fmt.Fprintf(&ddBuf, " L%.1f,%.1f", toX(i), toY(ep[i]))
	}
	ddBuf.WriteString(" Z")
	w(`<path d="%s" style="fill:var(--red);fill-opacity:0.1"/>`, ddBuf.String())

	// Area fill under equity line (gradient).
	var areaPath strings.Builder
	fmt.Fprintf(&areaPath, "M%.1f,%.1f", toX(0), toY(ep[0]))
	for i := 1; i < n; i++ {
		fmt.Fprintf(&areaPath, " L%.1f,%.1f", toX(i), toY(ep[i]))
	}
	fmt.Fprintf(&areaPath, " L%.1f,%.1f L%.1f,%.1f Z", toX(n-1), toY(minY), toX(0), toY(minY))
	w(`<path d="%s" fill="url(#eqGrad)"/>`, areaPath.String())

	// Peak line (dashed).
	var peakBuf strings.Builder
	fmt.Fprintf(&peakBuf, "M%.1f,%.1f", toX(0), toY(pp[0]))
	for i := 1; i < n; i++ {
		fmt.Fprintf(&peakBuf, " L%.1f,%.1f", toX(i), toY(pp[i]))
	}
	w(`<path d="%s" fill="none" style="stroke:var(--text-dim)" stroke-width="1" stroke-dasharray="3,3"/>`, peakBuf.String())

	// Equity line.
	var eqBuf strings.Builder
	fmt.Fprintf(&eqBuf, "M%.1f,%.1f", toX(0), toY(ep[0]))
	for i := 1; i < n; i++ {
		fmt.Fprintf(&eqBuf, " L%.1f,%.1f", toX(i), toY(ep[i]))
	}
	w(`<path d="%s" fill="none" style="stroke:%s" stroke-width="2"/>`, eqBuf.String(), lineVar)

	// Trade dots.
	for i, v := range ep {
		c := "var(--green)"
		if i > 0 && ep[i] < ep[i-1] {
			c = "var(--red)"
		}
		w(`<circle cx="%.1f" cy="%.1f" r="3" style="fill:%s" opacity="0.8"><title>#%d $%+.2f</title></circle>`,
			toX(i), toY(v), c, i+1, v)
	}

	// X axis labels.
	w(`<text x="%.1f" y="%d" style="fill:var(--text-dim)" font-size="10" text-anchor="middle">#1</text>`,
		toX(0), chartH-5)
	w(`<text x="%.1f" y="%d" style="fill:var(--text-dim)" font-size="10" text-anchor="middle">#%d</text>`,
		toX(n-1), chartH-5, n)

	// Legend.
	w(`<text x="%d" y="%d" style="fill:%s" font-size="10">— equity</text>`, padL+4, padT+12, lineVar)
	w(`<text x="%d" y="%d" style="fill:var(--text-dim)" font-size="10">--- peak</text>`, padL+70, padT+12)
	w(`<rect x="%d" y="%d" width="10" height="10" style="fill:var(--red);fill-opacity:0.12"/>`, padL+120, padT+3)
	w(`<text x="%d" y="%d" style="fill:var(--text-dim)" font-size="10">drawdown</text>`, padL+134, padT+12)

	w(`</svg>`)
	w(`</div>`)

	return template.HTML(buf.String())
}

// renderCompactEquitySVG renders a compact equity curve SVG for multi-price detail sections.
func renderCompactEquitySVG(metrics PerformanceMetrics) template.HTML {
	ep := metrics.EquityPoints
	pp := metrics.PeakPoints
	n := len(ep)
	if n < 2 {
		return ""
	}

	var buf bytes.Buffer
	w := func(format string, args ...any) {
		fmt.Fprintf(&buf, format+"\n", args...)
	}

	const chartW, chartH = 700, 160
	const padL, padR, padT, padB = 50, 15, 8, 24

	plotW := float64(chartW - padL - padR)
	plotH := float64(chartH - padT - padB)

	minY, maxY := 0.0, 0.0
	for _, v := range ep {
		if v < minY {
			minY = v
		}
		if v > maxY {
			maxY = v
		}
	}
	for _, v := range pp {
		if v > maxY {
			maxY = v
		}
	}
	spread := maxY - minY
	if spread == 0 {
		spread = 1
	}
	minY -= spread * 0.1
	maxY += spread * 0.1
	yRange := maxY - minY

	toX := func(i int) float64 { return float64(padL) + float64(i)/float64(n-1)*plotW }
	toY := func(v float64) float64 { return float64(padT) + (1-(v-minY)/yRange)*plotH }

	w(`<svg viewBox="0 0 %d %d" class="w-full h-auto rounded-lg border border-slate-800/50 mt-2" style="max-width:%dpx;background:var(--bg-surface)">`,
		chartW, chartH, chartW)

	// Gradient definition.
	lineVar := "var(--green)"
	if ep[n-1] < 0 {
		lineVar = "var(--red)"
	}
	w(`<defs><linearGradient id="eqGrad2" x1="0" y1="0" x2="0" y2="1">`)
	w(`<stop offset="0%%" style="stop-color:%s;stop-opacity:0.25"/>`, lineVar)
	w(`<stop offset="100%%" style="stop-color:%s;stop-opacity:0.02"/>`, lineVar)
	w(`</linearGradient></defs>`)

	// Horizontal grid lines.
	for i := 1; i < 3; i++ {
		gridY := minY + yRange*float64(i)/3.0
		py := toY(gridY)
		w(`<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" style="stroke:var(--chart-grid)" stroke-width="0.5"/>`,
			padL, py, chartW-padR, py)
	}

	// Zero line.
	zeroY := toY(0)
	w(`<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" style="stroke:var(--chart-grid-zero)" stroke-width="1" stroke-dasharray="3,3"/>`,
		padL, zeroY, chartW-padR, zeroY)
	w(`<text x="%d" y="%.1f" style="fill:var(--text-dim)" font-size="9" text-anchor="end" dominant-baseline="middle">$0</text>`,
		padL-4, zeroY)

	// Drawdown fill.
	var ddBuf strings.Builder
	fmt.Fprintf(&ddBuf, "M%.1f,%.1f", toX(0), toY(pp[0]))
	for i := 1; i < n; i++ {
		fmt.Fprintf(&ddBuf, " L%.1f,%.1f", toX(i), toY(pp[i]))
	}
	for i := n - 1; i >= 0; i-- {
		fmt.Fprintf(&ddBuf, " L%.1f,%.1f", toX(i), toY(ep[i]))
	}
	ddBuf.WriteString(" Z")
	w(`<path d="%s" style="fill:var(--red);fill-opacity:0.10"/>`, ddBuf.String())

	// Area fill under equity line.
	var areaPath strings.Builder
	fmt.Fprintf(&areaPath, "M%.1f,%.1f", toX(0), toY(ep[0]))
	for i := 1; i < n; i++ {
		fmt.Fprintf(&areaPath, " L%.1f,%.1f", toX(i), toY(ep[i]))
	}
	fmt.Fprintf(&areaPath, " L%.1f,%.1f L%.1f,%.1f Z", toX(n-1), toY(minY), toX(0), toY(minY))
	w(`<path d="%s" fill="url(#eqGrad2)"/>`, areaPath.String())

	// Equity line.
	var eqBuf strings.Builder
	fmt.Fprintf(&eqBuf, "M%.1f,%.1f", toX(0), toY(ep[0]))
	for i := 1; i < n; i++ {
		fmt.Fprintf(&eqBuf, " L%.1f,%.1f", toX(i), toY(ep[i]))
	}
	w(`<path d="%s" fill="none" style="stroke:%s" stroke-width="1.5"/>`, eqBuf.String(), lineVar)

	// End label.
	w(`<text x="%.1f" y="%.1f" style="fill:%s" font-size="9" text-anchor="start" dominant-baseline="middle">$%+.2f</text>`,
		toX(n-1)+4, toY(ep[n-1]), lineVar, ep[n-1])

	w(`</svg>`)

	return template.HTML(buf.String())
}

// ---------------------------------------------------------------------------
// Bucket builders
// ---------------------------------------------------------------------------

// buildDirectionBuckets groups resolved trades by side (Up/Down).
func (pt *PaperTrader) buildDirectionBuckets() []priceBucket {
	buckets := []priceBucket{
		{label: "Up"},
		{label: "Down"},
	}
	for _, t := range pt.trades {
		if !t.Resolved {
			continue
		}
		idx := -1
		switch t.Side {
		case "Up":
			idx = 0
		case "Down":
			idx = 1
		}
		if idx < 0 {
			continue
		}
		if t.Won {
			buckets[idx].wins++
		} else {
			buckets[idx].losses++
		}
		buckets[idx].totalPnL += t.PnL
	}
	return buckets
}

// buildRemainingBuckets groups resolved trades by remaining time at entry.
func (pt *PaperTrader) buildRemainingBuckets() []priceBucket {
	buckets := []priceBucket{
		{label: "0 - 60s", lo: 0, hi: 60},
		{label: "60 - 120s", lo: 60, hi: 120},
		{label: "120 - 180s", lo: 120, hi: 180},
		{label: "180 - 240s", lo: 180, hi: 240},
		{label: "240 - 300s", lo: 240, hi: 300},
	}
	for _, t := range pt.trades {
		if !t.Resolved {
			continue
		}
		secs := t.Remaining.Seconds()
		for i := range buckets {
			if secs >= buckets[i].lo && secs < buckets[i].hi {
				if t.Won {
					buckets[i].wins++
				} else {
					buckets[i].losses++
				}
				buckets[i].totalPnL += t.PnL
				break
			}
		}
	}
	return buckets
}

// buildPriceBuckets groups resolved trades into buy-price ranges.
func (pt *PaperTrader) buildPriceBuckets() []priceBucket {
	buckets := make([]priceBucket, 9)
	for i := range buckets {
		lo := 0.10 + float64(i)*0.10
		hi := lo + 0.10
		buckets[i] = priceBucket{
			label: fmt.Sprintf("%.2f - %.2f", lo, hi),
			lo:    lo,
			hi:    hi,
		}
	}
	for _, t := range pt.trades {
		if !t.Resolved {
			continue
		}
		for i := range buckets {
			if t.BuyPrice >= buckets[i].lo && t.BuyPrice < buckets[i].hi {
				if t.Won {
					buckets[i].wins++
				} else {
					buckets[i].losses++
				}
				buckets[i].totalPnL += t.PnL
				break
			}
		}
	}
	return buckets
}

// buildChangeBuckets groups resolved trades by absolute 5m price change.
func (pt *PaperTrader) buildChangeBuckets() []priceBucket {
	buckets := []priceBucket{
		{label: "$10 - $30", lo: 10, hi: 30},
		{label: "$30 - $50", lo: 30, hi: 50},
		{label: "$50 - $100", lo: 50, hi: 100},
		{label: "$100 - $200", lo: 100, hi: 200},
		{label: "$200 - $500", lo: 200, hi: 500},
		{label: "$500+", lo: 500, hi: math.MaxFloat64},
	}
	for _, t := range pt.trades {
		if !t.Resolved {
			continue
		}
		absChange := math.Abs(t.Change5m)
		for i := range buckets {
			if absChange >= buckets[i].lo && absChange < buckets[i].hi {
				if t.Won {
					buckets[i].wins++
				} else {
					buckets[i].losses++
				}
				buckets[i].totalPnL += t.PnL
				break
			}
		}
	}
	return buckets
}

// ---------------------------------------------------------------------------
// Performance metrics
// ---------------------------------------------------------------------------

// PerformanceMetrics holds industry-standard risk and return metrics.
type PerformanceMetrics struct {
	SharpeRatio    float64 // annualized risk-adjusted return
	MaxDrawdown    float64 // peak-to-trough max loss ($)
	MaxDrawdownPct float64 // max drawdown as percentage of peak equity
	ProfitFactor   float64 // gross profit / gross loss
	Expectancy     float64 // expected $ per trade
	AvgWin         float64
	AvgLoss        float64
	WinLossRatio   float64 // avg win / avg loss (payoff ratio)
	MaxConsecWins  int
	MaxConsecLoss  int
	RecoveryFactor float64 // net profit / max drawdown
	// Equity curve data for SVG chart.
	EquityPoints   []float64 // cumulative P&L after each resolved trade
	PeakPoints     []float64 // running peak at each point (for drawdown shading)
	DrawdownPoints []float64 // drawdown at each point ($)
}

// computeMetrics calculates all performance metrics from resolved trades.
func (pt *PaperTrader) computeMetrics() PerformanceMetrics {
	var m PerformanceMetrics

	var resolvedPnLs []float64
	var grossWin, grossLoss float64
	var winCount, lossCount int

	for _, t := range pt.trades {
		if !t.Resolved {
			continue
		}
		resolvedPnLs = append(resolvedPnLs, t.PnL)
		if t.Won {
			grossWin += t.PnL
			winCount++
		} else {
			grossLoss += math.Abs(t.PnL)
			lossCount++
		}
	}

	n := len(resolvedPnLs)
	if n == 0 {
		return m
	}

	// Avg Win / Avg Loss / Win-Loss Ratio.
	if winCount > 0 {
		m.AvgWin = grossWin / float64(winCount)
	}
	if lossCount > 0 {
		m.AvgLoss = grossLoss / float64(lossCount)
	}
	if m.AvgLoss > 0 {
		m.WinLossRatio = m.AvgWin / m.AvgLoss
	}

	// Profit Factor.
	if grossLoss > 0 {
		m.ProfitFactor = grossWin / grossLoss
	}

	// Expectancy = winRate * avgWin - lossRate * avgLoss.
	winRate := float64(winCount) / float64(n)
	lossRate := float64(lossCount) / float64(n)
	m.Expectancy = winRate*m.AvgWin - lossRate*m.AvgLoss

	// Sharpe Ratio: mean(PnL) / stddev(PnL) * sqrt(trades_per_day * 365).
	// 5-minute windows -> up to 288 trades/day.
	meanPnL := pt.totalPnL / float64(n)
	if n >= 2 {
		var variance float64
		for _, p := range resolvedPnLs {
			d := p - meanPnL
			variance += d * d
		}
		sd := math.Sqrt(variance / float64(n-1))
		if sd > 0 {
			annualFactor := math.Sqrt(288 * 365)
			m.SharpeRatio = (meanPnL / sd) * annualFactor
		}
	}

	// Equity curve, peak tracking, max drawdown.
	cum := 0.0
	peak := 0.0
	for _, p := range resolvedPnLs {
		cum += p
		m.EquityPoints = append(m.EquityPoints, cum)
		if cum > peak {
			peak = cum
		}
		m.PeakPoints = append(m.PeakPoints, peak)
		dd := peak - cum
		m.DrawdownPoints = append(m.DrawdownPoints, dd)
		if dd > m.MaxDrawdown {
			m.MaxDrawdown = dd
		}
	}
	if peak > 0 {
		m.MaxDrawdownPct = m.MaxDrawdown / peak * 100
	}

	// Recovery Factor.
	if m.MaxDrawdown > 0 {
		m.RecoveryFactor = pt.totalPnL / m.MaxDrawdown
	}

	// Max consecutive wins / losses.
	streak := 0
	firstResolved := true
	var isWinStreak bool
	for _, t := range pt.trades {
		if !t.Resolved {
			continue
		}
		if firstResolved || t.Won != isWinStreak {
			isWinStreak = t.Won
			streak = 1
			firstResolved = false
		} else {
			streak++
		}
		if isWinStreak && streak > m.MaxConsecWins {
			m.MaxConsecWins = streak
		}
		if !isWinStreak && streak > m.MaxConsecLoss {
			m.MaxConsecLoss = streak
		}
	}

	return m
}

// SortedPrices returns all resolved buy prices sorted ascending.
func (pt *PaperTrader) SortedPrices() []float64 {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	var prices []float64
	for _, t := range pt.trades {
		if t.Resolved {
			prices = append(prices, t.BuyPrice)
		}
	}
	sort.Float64s(prices)
	return prices
}

func wonStr(won bool) string {
	if won {
		return "WIN"
	}
	return "LOSS"
}

func wrColor(wr float64) template.CSS {
	if wr >= 70 {
		return "var(--green)"
	}
	if wr >= 50 {
		return "var(--amber)"
	}
	return "var(--red)"
}

func pnlFmtColor(pnl float64) template.CSS {
	if pnl >= 0 {
		return "var(--green)"
	}
	return "var(--red)"
}

func sharpeColor(sr float64) template.CSS {
	if sr >= 2 {
		return "var(--green)"
	}
	if sr >= 1 {
		return "var(--amber)"
	}
	return "var(--red)"
}

func profitFactorColor(pf float64) template.CSS {
	if pf >= 1.5 {
		return "var(--green)"
	}
	if pf >= 1.0 {
		return "var(--amber)"
	}
	return "var(--red)"
}

// ---------------------------------------------------------------------------
// Multi-price report
// ---------------------------------------------------------------------------

// MultiPaperHandler serves a combined HTML report for multiple paper traders.
type MultiPaperHandler struct {
	papers []*PaperTrader
	store  *store.Store // may be nil; used to query historical sessions
	dryRun bool         // true = engine is in dry-run mode (live slots are paper-only)

	listenersMu sync.Mutex
	listeners   []chan struct{}
}

// Notify wakes all connected SSE clients to re-render immediately.
// Non-blocking: if a client already has a pending notification, it is skipped.
func (mp *MultiPaperHandler) Notify() {
	mp.listenersMu.Lock()
	for _, ch := range mp.listeners {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	mp.listenersMu.Unlock()
}

// addListener registers a new SSE client and returns its notification channel.
func (mp *MultiPaperHandler) addListener() chan struct{} {
	ch := make(chan struct{}, 1)
	mp.listenersMu.Lock()
	mp.listeners = append(mp.listeners, ch)
	mp.listenersMu.Unlock()
	return ch
}

// removeListener unregisters an SSE client's notification channel.
func (mp *MultiPaperHandler) removeListener(ch chan struct{}) {
	mp.listenersMu.Lock()
	for i, l := range mp.listeners {
		if l == ch {
			mp.listeners = append(mp.listeners[:i], mp.listeners[i+1:]...)
			break
		}
	}
	mp.listenersMu.Unlock()
}

// NewMultiPaperHandler creates a handler for a multi-price paper trading report.
// dryRun indicates the engine is in dry-run mode; live slots will be labelled accordingly.
func NewMultiPaperHandler(papers []*PaperTrader, st *store.Store, dryRun bool) *MultiPaperHandler {
	return &MultiPaperHandler{papers: papers, store: st, dryRun: dryRun}
}

// ServeHTTP generates the combined multi-price report (full page, loaded once).
func (mp *MultiPaperHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	funcMap := TemplateFuncMap()
	tmpl := web.Templates(funcMap)

	if len(mp.papers) == 1 {
		pt := mp.papers[0]
		pt.mu.RLock()
		data := pt.buildPageData()
		pt.mu.RUnlock()
		data.DryRun = mp.dryRun
		mp.populateWindowResults(&data)
		mp.populateHistory(&data)
		if err := tmpl.ExecuteTemplate(w, "paper", data); err != nil {
			slog.Error("template execution failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	data := mp.buildMultiPageData()
	if err := tmpl.ExecuteTemplate(w, "multi", data); err != nil {
		slog.Error("template execution failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// renderFragment renders only the dynamic content block (header + main)
// without the layout shell. Returns the HTML bytes.
func (mp *MultiPaperHandler) renderFragment() ([]byte, error) {
	funcMap := TemplateFuncMap()
	tmpl := web.Templates(funcMap)
	var buf bytes.Buffer

	if len(mp.papers) == 1 {
		pt := mp.papers[0]
		pt.mu.RLock()
		data := pt.buildPageData()
		pt.mu.RUnlock()
		data.DryRun = mp.dryRun
		mp.populateWindowResults(&data)
		mp.populateHistory(&data)
		if err := tmpl.ExecuteTemplate(&buf, "paper_content", data); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	data := mp.buildMultiPageData()
	if err := tmpl.ExecuteTemplate(&buf, "multi_content", data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ServeSSE handles a Server-Sent Events connection.
// Updates are event-driven: pushed only when Notify() is called (trade, resolve, etc.).
// A 30-second keepalive prevents proxy/browser timeouts.
// Time-dependent fields (duration, current time) are updated client-side.
func (mp *MultiPaperHandler) ServeSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	changes := mp.addListener()
	defer mp.removeListener(changes)

	var lastHash uint64

	// Send initial fragment immediately.
	if html, err := mp.renderFragment(); err == nil {
		h := fnv.New64a()
		h.Write(html)
		lastHash = h.Sum64()
		writeSSE(w, html)
		flusher.Flush()
	}

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
			// Event-driven: trade recorded, window resolved, correction, etc.
			html, err := mp.renderFragment()
			if err != nil {
				slog.Debug("sse render error", "err", err)
				continue
			}
			h := fnv.New64a()
			h.Write(html)
			hash := h.Sum64()
			if hash == lastHash {
				continue
			}
			lastHash = hash
			writeSSE(w, html)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// writeSSE writes an SSE data event with multi-line HTML content.
func writeSSE(w http.ResponseWriter, html []byte) {
	// SSE data lines: each line prefixed with "data: ".
	// Use a single data field with the entire HTML.
	fmt.Fprintf(w, "data: %s\n\n", bytes.ReplaceAll(html, []byte("\n"), []byte("\ndata: ")))
}

// populateWindowResults fills window result fields on a PageData.
func (mp *MultiPaperHandler) populateWindowResults(data *PageData) {
	rows, count, wins, losses := mp.buildWindowResultRows()
	data.WindowResults = rows
	data.WindowCount = count
	data.WindowUpCount = wins
	data.WindowDownCount = losses
	data.HasWindowResults = len(rows) > 0
}

// populateHistory fills history session fields on a PageData.
func (mp *MultiPaperHandler) populateHistory(data *PageData) {
	sessions := mp.buildHistoryData()
	data.HistorySessions = sessions
	data.HasHistory = len(sessions) > 0
}

// buildMultiPageData prepares all view data for the multi-price report.
func (mp *MultiPaperHandler) buildMultiPageData() MultiPageData {
	// Compute aggregate stats, split by live vs paper.
	startTime := time.Now()
	var totalPnL float64
	var totalWins, totalLosses, totalTrades int
	var liveStats, paperStats AggStats
	for _, pt := range mp.papers {
		pt.mu.RLock()
		if pt.startTime.Before(startTime) {
			startTime = pt.startTime
		}
		totalPnL += pt.totalPnL
		totalWins += pt.wins
		totalLosses += pt.losses
		totalTrades += len(pt.trades)
		agg := &paperStats
		if pt.live {
			agg = &liveStats
		}
		agg.Trades += len(pt.trades)
		agg.Wins += pt.wins
		agg.Losses += pt.losses
		agg.TotalPnL += pt.totalPnL
		pt.mu.RUnlock()
	}
	duration := time.Since(startTime).Truncate(time.Second)
	totalResolved := totalWins + totalLosses
	aggWinRate := 0.0
	if totalResolved > 0 {
		aggWinRate = float64(totalWins) / float64(totalResolved) * 100
	}

	// Finalize per-group stats.
	for _, s := range []*AggStats{&liveStats, &paperStats} {
		s.Resolved = s.Wins + s.Losses
		if s.Resolved > 0 {
			s.WinRate = float64(s.Wins) / float64(s.Resolved) * 100
			s.AvgPnL = s.TotalPnL / float64(s.Resolved)
			s.HasResolved = true
		}
	}

	data := MultiPageData{
		ActivePage:  "paper",
		Title:       "Multi-Price Paper Trading",
		StartTime:   startTime.Format("2006-01-02 15:04:05"),
		StartUnix:   startTime.Unix(),
		CurrentTime: time.Now().Format("2006-01-02 15:04:05"),
		Duration:    duration,
		SlotCount:   len(mp.papers),
		TotalPnL:    totalPnL,
		AggWinRate:  aggWinRate,
		TotalTrades: totalTrades,
		HasResolved: totalResolved > 0,
		TradeCount:  totalTrades,
		WinRate:     aggWinRate,
		LiveStats:   liveStats,
		PaperStats:  paperStats,
		HasLive:     liveStats.Trades > 0,
		HasPaper:    paperStats.Trades > 0,
		DryRun:      mp.dryRun,
		ServerTZ:    serverTZString(),
	}

	// Window results.
	rows, count, wins, losses := mp.buildWindowResultRows()
	data.WindowResults = rows
	data.WindowCount = count
	data.WindowUpCount = wins
	data.WindowDownCount = losses
	data.HasWindowResults = len(rows) > 0

	// Find best PnL for highlighting.
	bestPnL := -math.MaxFloat64
	for _, pt := range mp.papers {
		pt.mu.RLock()
		if pt.totalPnL > bestPnL && (pt.wins+pt.losses) > 0 {
			bestPnL = pt.totalPnL
		}
		pt.mu.RUnlock()
	}

	// Build slot summaries and detail panels.
	for _, pt := range mp.papers {
		pt.mu.RLock()
		resolved := pt.wins + pt.losses
		trades := len(pt.trades)
		winRate := 0.0
		avgPnL := 0.0
		if resolved > 0 {
			winRate = float64(pt.wins) / float64(resolved) * 100
			avgPnL = pt.totalPnL / float64(resolved)
		}
		metrics := pt.computeMetrics()

		slot := SlotSummaryRow{
			Label:       pt.label,
			Live:        pt.live,
			Trades:      trades,
			Wins:        pt.wins,
			Losses:      pt.losses,
			Resolved:    resolved,
			WinRate:     winRate,
			AvgPnL:      avgPnL,
			TotalPnL:    pt.totalPnL,
			IsBest:      resolved > 0 && pt.totalPnL == bestPnL,
			Metrics:     toMetricsData(metrics),
			HasResolved: resolved > 0,
			LastTrade:   toLastTradeInfo(pt.lastTrade(), false),
		}
		data.Slots = append(data.Slots, slot)

		detail := SlotDetailData{
			Label:       pt.label,
			Live:        pt.live,
			Trades:      trades,
			Wins:        pt.wins,
			Losses:      pt.losses,
			Resolved:    resolved,
			WinRate:     winRate,
			TotalPnL:    pt.totalPnL,
			AvgPnL:      avgPnL,
			HasResolved: resolved > 0,
			Metrics:     toMetricsData(metrics),
			LastTrade:   toLastTradeInfo(pt.lastTrade(), true),
		}

		if resolved > 1 && len(metrics.EquityPoints) > 1 {
			detail.EquitySVG = renderCompactEquitySVG(metrics)
		}
		if resolved > 0 {
			detail.DirectionBuckets = toBucketRows(pt.buildDirectionBuckets())
			detail.TimingBuckets = toBucketRows(pt.buildRemainingBuckets())
		}
		if trades > 0 {
			detail.TradeHistory = toTradeRows(pt.trades)
		}

		data.SlotDetails = append(data.SlotDetails, detail)
		pt.mu.RUnlock()
	}

	// History.
	sessions := mp.buildHistoryData()
	data.HistorySessions = sessions
	data.HasHistory = len(sessions) > 0

	return data
}

// buildWindowResultRows returns window result data for templates.
func (mp *MultiPaperHandler) buildWindowResultRows() ([]WindowResultRow, int, int, int) {
	if mp.store == nil {
		return nil, 0, 0, 0
	}
	sessionID := mp.store.SessionID()
	windows, err := mp.store.QuerySessionWindows(sessionID)
	if err != nil {
		slog.Warn("failed to query session windows for report", "err", err)
		return nil, 0, 0, 0
	}
	if len(windows) == 0 {
		return nil, 0, 0, 0
	}

	// Build set of traded windows with slot labels and sides.
	tradedWindowSlots := make(map[string][]TradedSlot) // window key -> traded slots
	for _, pt := range mp.papers {
		pt.mu.RLock()
		seen := make(map[string]bool) // dedup per slot per window
		for _, t := range pt.trades {
			key := t.WindowStart.Format(time.RFC3339)
			if !seen[key] {
				tradedWindowSlots[key] = append(tradedWindowSlots[key], TradedSlot{
					Label: pt.label,
					Side:  t.Side,
				})
				seen[key] = true
			}
		}
		pt.mu.RUnlock()
	}

	// Build rows newest-first, counting up/down directions.
	upCount, downCount := 0, 0
	rows := make([]WindowResultRow, len(windows))
	for i := len(windows) - 1; i >= 0; i-- {
		w := windows[i]
		key := w.StartTime.Format(time.RFC3339)
		slots := tradedWindowSlots[key]

		// Determine win/lose for each traded slot.
		var result string
		for j := range slots {
			slots[j].Won = slots[j].Side == w.Direction
		}
		if len(slots) > 0 {
			if slots[0].Won {
				result = "Win"
			} else {
				result = "Lose"
			}
		}

		// Count direction.
		if w.Direction == "Up" {
			upCount++
		} else {
			downCount++
		}

		rows[len(windows)-1-i] = WindowResultRow{
			EndTime:      w.StartTime.Add(5 * time.Minute).Format("15:04:05"),
			Direction:    w.Direction,
			Result:       result,
			MarketSignal: w.MarketSignal,
			BTCOpen:      w.BTCOpen,
			BTCClose:     w.BTCClose,
			Change:       w.Change,
			Traded:       len(slots) > 0,
			TradedSlots:  slots,
		}
	}

	return rows, len(windows), upCount, downCount
}

// slotStats holds per-slot aggregated statistics.
type slotStats struct {
	Slot   string
	Trades int
	Wins   int
	Losses int
	PnL    float64
	Live   bool
}

// buildHistoryData prepares historical session data for templates.
func (mp *MultiPaperHandler) buildHistoryData() []HistorySessionRow {
	if mp.store == nil {
		return nil
	}
	sessionID := mp.store.SessionID()
	summaries, err := mp.store.PaperSessionSummaries(sessionID, 20)
	if err != nil {
		slog.Warn("failed to query paper session summaries for report", "err", err)
		return nil
	}
	if len(summaries) == 0 {
		return nil
	}

	var sessions []HistorySessionRow
	for _, s := range summaries {
		dur := s.EndedAt.Sub(s.StartedAt).Truncate(time.Second)
		resolved := s.Wins + s.Losses
		winRate := 0.0
		if resolved > 0 {
			winRate = float64(s.Wins) / float64(resolved) * 100
		}

		session := HistorySessionRow{
			SessionID: s.SessionID,
			StartedAt: s.StartedAt.Format("01-02 15:04"),
			Duration:  dur,
			Trades:    s.Trades,
			Wins:      s.Wins,
			Losses:    s.Losses,
			Resolved:  resolved,
			WinRate:   winRate,
			TotalPnL:  s.TotalPnL,
		}

		// Fetch trades for expanded view.
		trades, fetchErr := mp.store.PaperSessionTrades(s.SessionID)
		if fetchErr != nil {
			slog.Warn("failed to query paper session trades for report", "session", s.SessionID, "err", fetchErr)
			session.FetchError = true
			sessions = append(sessions, session)
			continue
		}

		// Per-slot summary.
		slotMap := make(map[string]*slotStats)
		var slotOrder []string
		for _, t := range trades {
			ss, ok := slotMap[t.SlotLabel]
			if !ok {
				ss = &slotStats{Slot: t.SlotLabel, Live: t.Live}
				slotMap[t.SlotLabel] = ss
				slotOrder = append(slotOrder, t.SlotLabel)
			}
			ss.Trades++
			if t.Resolved {
				if t.Won {
					ss.Wins++
				} else {
					ss.Losses++
				}
				ss.PnL += t.PnL
			}
		}
		for _, key := range slotOrder {
			ss := slotMap[key]
			slotResolved := ss.Wins + ss.Losses
			slotWR := 0.0
			if slotResolved > 0 {
				slotWR = float64(ss.Wins) / float64(slotResolved) * 100
			}
			session.SlotSummaries = append(session.SlotSummaries, HistorySlotRow{
				Slot:     ss.Slot,
				Trades:   ss.Trades,
				Wins:     ss.Wins,
				Losses:   ss.Losses,
				Resolved: slotResolved,
				WinRate:  slotWR,
				PnL:      ss.PnL,
				Live:     ss.Live,
			})
		}

		// Trade details.
		for i, t := range trades {
			remaining := time.Duration(t.RemainingNs)
			session.TradeDetails = append(session.TradeDetails, HistoryTradeRow{
				Number:    i + 1,
				Time:      t.EntryTime.Format("15:04:05"),
				SlotLabel: t.SlotLabel,
				Side:      t.Side,
				BuyPrice:  t.BuyPrice,
				Change5m:  t.Change5m,
				Remaining: int(remaining.Seconds()),
				Resolved:  t.Resolved,
				Won:       t.Won,
				FinalDir:  t.FinalDir,
				PnL:       t.PnL,
				Live:      t.Live,
			})
		}
		session.HasTrades = len(trades) > 0

		sessions = append(sessions, session)
	}

	return sessions
}
