package engine

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------
// Conversion helpers: viewmodel primitives → API types
// ---------------------------------------------------------------------------

func toAPIMetrics(m PerformanceMetrics) APIMetrics {
	return APIMetrics{
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

func toAPIBucketRows(buckets []priceBucket) []APIBucketRow {
	var rows []APIBucketRow
	for _, b := range buckets {
		n := b.wins + b.losses
		if n == 0 {
			continue
		}
		rows = append(rows, APIBucketRow{
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

func toAPITradeRows(trades []PaperTrade) []APITradeRow {
	rows := make([]APITradeRow, len(trades))
	for i, t := range trades {
		row := APITradeRow{
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
		if t.FinalDir == "early_exit" && t.Size > 0 {
			row.SellPrice = t.BuyPrice + t.PnL/t.Size
		}
		rows[i] = row
	}
	return rows
}

func toAPIHoldReasons(holdReasons map[string]int, evalCount int) []APIHoldReason {
	hrs := sortedHoldReasons(holdReasons, evalCount)
	if hrs == nil {
		return nil
	}
	rows := make([]APIHoldReason, len(hrs))
	for i, h := range hrs {
		rows[i] = APIHoldReason{Reason: h.Reason, Count: h.Count, Pct: h.Pct}
	}
	return rows
}

func toAPIProfitRows(wins, losses, resolved int, observedWR float64) []APIProfitRow {
	simPrices := []float64{0.50, 0.55, 0.60, 0.65, 0.70, 0.75, 0.80, 0.85, 0.90, 0.95}
	rows := make([]APIProfitRow, len(simPrices))
	for i, p := range simPrices {
		winProfit := (1 - p) / p
		totalPnL := float64(wins)*winProfit - float64(losses)*1.0
		perTrade := totalPnL / float64(resolved)
		rows[i] = APIProfitRow{
			BuyPrice:     p,
			WinProfit:    winProfit,
			TotalPnL:     totalPnL,
			PerTrade:     perTrade,
			NeedWR:       p * 100,
			IsProfitable: totalPnL > 0,
			IsBreakeven:  p == observedWR,
			BarWidth:     math.Min(math.Abs(totalPnL)/10*100, 100),
		}
	}
	return rows
}

func toAPIWindows(rows []WindowResultRow, count, up, down int) APIWindows {
	apiRows := make([]APIWindowRow, len(rows))
	for i, r := range rows {
		var slots []APITradedSlot
		for _, s := range r.TradedSlots {
			slots = append(slots, APITradedSlot{Label: s.Label, Side: s.Side, Won: s.Won})
		}
		apiRows[i] = APIWindowRow{
			EndTime:      r.EndTime,
			Direction:    r.Direction,
			Result:       r.Result,
			MarketSignal: r.MarketSignal,
			BTCOpen:      r.BTCOpen,
			BTCClose:     r.BTCClose,
			Change:       r.Change,
			Traded:       r.Traded,
			TradedSlots:  slots,
		}
	}
	return APIWindows{Results: apiRows, Count: count, UpCount: up, DownCount: down}
}

func toAPILastTrade(lt *PaperTrade, longFormat bool) *APILastTrade {
	if lt == nil {
		return nil
	}
	timeFmt := "15:04"
	if longFormat {
		timeFmt = "15:04:05"
	}
	return &APILastTrade{
		Time:     lt.EntryTime.Format(timeFmt),
		Side:     lt.Side,
		BuyPrice: lt.BuyPrice,
		Resolved: lt.Resolved,
		Won:      lt.Won,
		FinalDir: lt.FinalDir,
		PnL:      lt.PnL,
		Ago:      time.Since(lt.EntryTime).Truncate(time.Second).String(),
	}
}

// ---------------------------------------------------------------------------
// JSON data builders
// ---------------------------------------------------------------------------

// buildSingleJSON builds a SingleData JSON response from a PaperTrader.
// The caller must hold pt.mu (at least RLock).
func (pt *PaperTrader) buildSingleJSON() SingleData {
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

	totalHolds := 0
	for _, v := range pt.holdReasons {
		totalHolds += v
	}

	data := SingleData{
		Meta: APIMeta{
			Title:     title,
			Label:     pt.label,
			StartTime: pt.startTime.Format("2006-01-02 15:04:05"),
			StartUnix: pt.startTime.Unix(),
			ServerTZ:  serverTZString(),
			EvalCount: pt.evalCount,
		},
		Summary: APISummary{
			TotalPnL:    pt.totalPnL,
			WinRate:     winRate,
			TradeCount:  len(pt.trades),
			Wins:        pt.wins,
			Losses:      pt.losses,
			Pending:     pending,
			AvgPnL:      avgPnL,
			Resolved:    resolved,
			HasResolved: resolved > 0,
			Duration:    duration.String(),
			Live:        pt.live,
		},
		Metrics:     toAPIMetrics(metrics),
		HoldReasons: toAPIHoldReasons(pt.holdReasons, pt.evalCount),
		TotalHolds:  totalHolds,
		Equity: APIEquity{
			Points:    metrics.EquityPoints,
			Peaks:     metrics.PeakPoints,
			Drawdowns: metrics.DrawdownPoints,
		},
		Trades: toAPITradeRows(pt.trades),
	}

	if resolved > 0 {
		data.Buckets = APIBuckets{
			Price:     toAPIBucketRows(pt.buildPriceBuckets()),
			Change:    toAPIBucketRows(pt.buildChangeBuckets()),
			Direction: toAPIBucketRows(pt.buildDirectionBuckets()),
			Timing:    toAPIBucketRows(pt.buildRemainingBuckets()),
		}
		observedWR := float64(pt.wins) / float64(resolved)
		data.ProfitSim = APIProfitSim{
			ObservedWR:     observedWR * 100,
			BreakevenPrice: observedWR,
			Rows:           toAPIProfitRows(pt.wins, pt.losses, resolved, observedWR),
		}
	}

	// Live/Paper split.
	agg := APIAgg{
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
		data.LivePaper.Live = agg
		data.LivePaper.HasLive = true
	} else {
		data.LivePaper.Paper = agg
		data.LivePaper.HasPaper = true
	}

	return data
}

// buildMultiJSON builds a MultiData JSON response.
func (mp *MultiPaperHandler) buildMultiJSON() MultiData {
	startTime := time.Now()
	var totalPnL float64
	var totalWins, totalLosses, totalTrades int
	var liveStats, paperStats APIAgg

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
	for _, s := range []*APIAgg{&liveStats, &paperStats} {
		s.Resolved = s.Wins + s.Losses
		if s.Resolved > 0 {
			s.WinRate = float64(s.Wins) / float64(s.Resolved) * 100
			s.AvgPnL = s.TotalPnL / float64(s.Resolved)
			s.HasResolved = true
		}
	}

	data := MultiData{
		Meta: APIMeta{
			Title:     "Multi-Price Paper Trading",
			StartTime: startTime.Format("2006-01-02 15:04:05"),
			StartUnix: startTime.Unix(),
			ServerTZ:  serverTZString(),
			SlotCount: len(mp.papers),
			DryRun:    mp.dryRun,
		},
		Summary: APIMultiSummary{
			TotalPnL:    totalPnL,
			WinRate:     aggWinRate,
			TradeCount:  totalTrades,
			HasResolved: totalResolved > 0,
			Duration:    duration.String(),
		},
		LivePaper: APILivePaper{
			HasLive:  liveStats.Trades > 0,
			HasPaper: paperStats.Trades > 0,
			Live:     liveStats,
			Paper:    paperStats,
		},
	}

	// Window results.
	rows, count, up, down := mp.buildWindowResultRows()
	data.Windows = toAPIWindows(rows, count, up, down)

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

		slot := APISlotSummary{
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
			Metrics:     toAPIMetrics(metrics),
			HasResolved: resolved > 0,
			LastTrade:   toAPILastTrade(pt.lastTrade(), false),
		}
		data.Slots = append(data.Slots, slot)

		detail := APISlotDetail{
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
			Metrics:     toAPIMetrics(metrics),
			LastTrade:   toAPILastTrade(pt.lastTrade(), true),
		}

		if len(metrics.EquityPoints) > 1 {
			detail.Equity = APIEquity{
				Points:    metrics.EquityPoints,
				Peaks:     metrics.PeakPoints,
				Drawdowns: metrics.DrawdownPoints,
			}
		}
		if resolved > 0 {
			detail.DirectionBuckets = toAPIBucketRows(pt.buildDirectionBuckets())
			detail.TimingBuckets = toAPIBucketRows(pt.buildRemainingBuckets())
		}
		if trades > 0 {
			detail.TradeHistory = toAPITradeRows(pt.trades)
		}

		data.Details = append(data.Details, detail)
		pt.mu.RUnlock()
	}

	// History.
	data.History = mp.buildHistoryJSON()

	return data
}

// buildHistoryJSON builds history session data in API format.
func (mp *MultiPaperHandler) buildHistoryJSON() []APIHistorySession {
	if mp.store == nil {
		return nil
	}
	sessionID := mp.store.SessionID()
	summaries, err := mp.store.PaperSessionSummaries(sessionID, 20)
	if err != nil {
		slog.Warn("failed to query paper session summaries for api", "err", err)
		return nil
	}
	if len(summaries) == 0 {
		return nil
	}

	var sessions []APIHistorySession
	for _, s := range summaries {
		dur := s.EndedAt.Sub(s.StartedAt).Truncate(time.Second)
		resolved := s.Wins + s.Losses
		winRate := 0.0
		if resolved > 0 {
			winRate = float64(s.Wins) / float64(resolved) * 100
		}

		session := APIHistorySession{
			SessionID: s.SessionID,
			StartedAt: s.StartedAt.Format("01-02 15:04"),
			Duration:  dur.String(),
			Trades:    s.Trades,
			Wins:      s.Wins,
			Losses:    s.Losses,
			Resolved:  resolved,
			WinRate:   winRate,
			TotalPnL:  s.TotalPnL,
		}

		trades, fetchErr := mp.store.PaperSessionTrades(s.SessionID)
		if fetchErr != nil {
			slog.Warn("failed to query paper session trades for api", "session", s.SessionID, "err", fetchErr)
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
			session.SlotSummaries = append(session.SlotSummaries, APIHistorySlot{
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
			session.TradeDetails = append(session.TradeDetails, APIHistoryTrade{
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

		sessions = append(sessions, session)
	}

	return sessions
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// buildAPIJSON builds the full API response as JSON bytes.
func (mp *MultiPaperHandler) buildAPIJSON() ([]byte, error) {
	var resp APIResponse
	if len(mp.papers) == 1 {
		resp.Mode = "single"
		pt := mp.papers[0]
		pt.mu.RLock()
		single := pt.buildSingleJSON()
		pt.mu.RUnlock()
		single.Meta.DryRun = mp.dryRun
		rows, count, up, down := mp.buildWindowResultRows()
		single.Windows = toAPIWindows(rows, count, up, down)
		single.History = mp.buildHistoryJSON()
		resp.Single = &single
	} else {
		resp.Mode = "multi"
		multi := mp.buildMultiJSON()
		resp.Multi = &multi
	}
	return json.Marshal(resp)
}

// ServeAPI handles GET /api/paper/data — returns full JSON snapshot.
func (mp *MultiPaperHandler) ServeAPI(w http.ResponseWriter, r *http.Request) {
	data, err := mp.buildAPIJSON()
	if err != nil {
		slog.Error("api json build error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// ServeSSEJSON handles SSE with JSON payloads (replaces old HTML-based ServeSSE).
func (mp *MultiPaperHandler) ServeSSEJSON(w http.ResponseWriter, r *http.Request) {
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

	// Send initial data immediately.
	if data, err := mp.buildAPIJSON(); err == nil {
		h := fnv.New64a()
		h.Write(data)
		lastHash = h.Sum64()
		fmt.Fprintf(w, "data: %s\n\n", data)
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
			data, err := mp.buildAPIJSON()
			if err != nil {
				slog.Debug("sse json build error", "err", err)
				continue
			}
			h := fnv.New64a()
			h.Write(data)
			hash := h.Sum64()
			if hash == lastHash {
				continue
			}
			lastHash = hash
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
