package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/easay/pmbot/internal/clob"
	"github.com/easay/pmbot/internal/config"
	"github.com/easay/pmbot/internal/feed"
	"github.com/easay/pmbot/internal/gamma"
	"github.com/easay/pmbot/internal/store"
	"github.com/easay/pmbot/internal/strategy"
	"github.com/easay/pmbot/internal/ws"
	"github.com/easay/pmbot/web"
)

// marketMeta holds static metadata for a resolved market.
type marketMeta struct {
	question    string
	conditionID string
	yesTokenID  string
	noTokenID   string
	tickSize    string
	negRisk     bool
	feeRateBps  int
	lowerBound  float64
	upperBound  float64
}

const (
	// Background sell goroutine settings.
	sellPollInterval   = 2 * time.Second
	sellWindowLeadTime = 1 * time.Minute // only sell in the last 1 minute of the current 5m window
	maxSellRetries     = 10
	maxSellAge         = 10 * time.Minute // allow 2 windows for retries

	// Max FAK retries for early exit sells before relayer fallback.
	maxEarlyExitRetries = 5

	// Dashboard heartbeat interval.
	dashboardTickInterval = 30 * time.Second
)

// paperSlot holds an independent paper-trading simulation at a specific entry price.
// Each slot has its own strategy instance, PaperTrader, position tracking, and
// window dedup state so multiple entry prices can be evaluated in parallel.
type paperSlot struct {
	entryPrice       float64
	live             bool // true = place real orders; false = paper-only
	strategy         strategy.Strategy
	paper            *PaperTrader
	lastBuyWindow    time.Time
	lastSignal       strategy.Action
	lastOrderedToken string
	positions        map[string]float64    // independent position tracking per slot
	marketsBuf       []strategy.MarketInfo // pre-allocated buffer for evaluation

	// Entry tracking for early exit stop-loss.
	entryPrices  map[string]float64       // token → actual fill price at entry
	entryElapsed map[string]time.Duration // token → window elapsed at entry

	// FAK retry state: track failures to allow retries with cooldown.
	windowRetries int       // consecutive FAK failures in current window
	lastFailedAt  time.Time // timestamp of last failed order attempt
}

// Engine orchestrates data feeds, strategy evaluation, and order execution.
type Engine struct {
	cfg        *config.Config
	clobClient *clob.Client

	// Pipeline components: strategy (in slots) → risk → order.
	riskMgr  *RiskManager
	orderMgr *OrderManager

	// Dynamic slug template (e.g., "btc-updown-5m-{5m_window}").
	slugTemplate string
	isDynamic    bool // true if slug contains {5m_window}
	currentSlug  string

	// All markets resolved for the current event.
	markets  []marketMeta
	wsClient *ws.Client

	// Live state.
	snapshots          *ws.SnapshotStore
	btcPrice           float64
	lastTickTime       time.Time // wall-clock time of the most recent price tick
	lastTickServerTime time.Time // server-side timestamp of the most recent price tick
	trend              *TrendTracker
	candles     *feed.CandleAggregator
	evalMarkets []strategy.MarketInfo // pre-allocated buffer for evaluate()

	// Dashboard state machine (event-driven + heartbeat).
	lastOrderedToken string          // dedup: last token for signal change detection
	lastSignal       strategy.Action // dedup: last effective action
	lastLogTime      time.Time       // heartbeat: last tick log time

	// Live position monitoring: fetched from Polymarket API for logging.
	posMu     sync.RWMutex
	positions map[string]float64

	// Multi-price slots: each slot evaluates and executes independently.
	paperSlots     []*paperSlot
	lastResolved5m time.Time // track last resolved 5m window to detect completion

	// Official resolution tracking: compare local candle direction with
	// the authoritative market_resolved WS event.
	lastLocalDir     string   // direction from local candle at last resolve
	prevMarketTokens []string // previous window's token IDs, kept for WS overlap

	// Background sell: positions queued for selling after their window completes.
	pendingSellsMu sync.Mutex
	pendingSells   []pendingSell

	// Data collection for backtesting analysis.
	collector *DataCollector
	store     *store.Store

	// Claim self-healing: track retry counts per conditionID.
	claimRetries map[string]int

	// SSE event push: notifies connected dashboard clients on state changes.
	paperHandler *MultiPaperHandler

	// Extra HTTP handler injected by caller (e.g., backtest page).
	extraHandler http.Handler
}

// pendingSell represents a position queued for selling after its window completes.
type pendingSell struct {
	slotLabel    string
	tokenID      string
	conditionID  string
	outcomeIndex int // 0 = Yes, 1 = No
	shares       float64
	tickSize     string
	negRisk      bool
	feeRateBps   int
	addedAt      time.Time
	retries      int
}

const defaultDBPath = "pmbot.db"

// PaperSlotConfig describes one slot with its own entry price and strategy.
// Live slots place real orders; non-live slots only record to PaperTrader.
type PaperSlotConfig struct {
	EntryPrice float64
	Live       bool // true = place real orders when dry_run is false
	Strategy   strategy.Strategy
}

// New creates a new Engine.
// paperSlotCfgs defines independent paper-trading simulations at different entry prices.
// If empty, a single slot is created from the primary strategy.
func New(cfg *config.Config, clobClient *clob.Client, paperSlotCfgs []PaperSlotConfig) *Engine {
	sessionID := time.Now().Format("20060102_150405")
	st, err := store.New(defaultDBPath, sessionID)
	if err != nil {
		slog.Warn("failed to open db, data will not be persisted", "path", defaultDBPath, "err", err)
	} else {
		sessions, windows, samples, _ := st.Stats()
		slog.Info("db opened", "path", defaultDBPath, "session", sessionID, "sessions", sessions, "windows", windows, "samples", samples)

		// Print historical paper session summaries.
		if summaries, err := st.PaperSessionSummaries(sessionID, 0); err == nil {
			for _, s := range summaries {
				slog.Info("previous session", "id", s.SessionID,
					"started", s.StartedAt.Format("2006-01-02 15:04:05"),
					"trades", s.Trades, "wins", s.Wins, "losses", s.Losses,
					"pnl", fmt.Sprintf("%.2f", s.TotalPnL))
			}
		}
		// Purge old sessions beyond retention limit.
		if err := st.PurgePaperSessions(cfg.MaxPaperSessions); err != nil {
			slog.Warn("failed to purge old paper sessions", "err", err)
		}
	}

	if len(paperSlotCfgs) == 0 {
		panic("at least one paper slot config is required")
	}
	slots := make([]*paperSlot, len(paperSlotCfgs))
	for i, sc := range paperSlotCfgs {
		label := fmt.Sprintf("$%.2f", sc.EntryPrice)
		if sc.EntryPrice == 0 {
			label = ""
		}
		pt := NewPaperTrader(label, sc.Live)
		slots[i] = &paperSlot{
			entryPrice:   sc.EntryPrice,
			live:         sc.Live,
			strategy:     sc.Strategy,
			paper:        pt,
			positions:    make(map[string]float64),
			entryPrices:  make(map[string]float64),
			entryElapsed: make(map[string]time.Duration),
		}
		slog.Info("slot created", "index", i, "label", label, "entry_price", sc.EntryPrice, "live", sc.Live)
	}

	return &Engine{
		cfg:          cfg,
		clobClient:   clobClient,
		riskMgr:      NewRiskManager(cfg.Execution.MaxDailyOrders, cfg.Execution.MaxDailyAmount),
		orderMgr:     NewOrderManager(clobClient, cfg.Execution.MaxSlippagePct, cfg.Execution.FAKPriceBufferTks),
		slugTemplate: cfg.Market.EventSlug,
		isDynamic:    config.Has5mWindow(cfg.Market.EventSlug),
		snapshots:    ws.NewSnapshotStore(),
		candles: feed.NewCandleAggregator(
			[]time.Duration{5 * time.Minute, 15 * time.Minute},
			20, // keep 20 completed candles per window
		),
		trend:      &TrendTracker{},
		positions:    make(map[string]float64),
		paperSlots:   slots,
		collector:    NewDataCollector(1*time.Second, st),
		store:        st,
		claimRetries: make(map[string]int),
	}
}

// SetExtraHandler registers an additional HTTP handler at /backtest.
// Must be called before Run().
func (e *Engine) SetExtraHandler(h http.Handler) {
	e.extraHandler = h
}

// Store returns the engine's data store (may be nil).
func (e *Engine) Store() *store.Store {
	return e.store
}

// Run starts all data sources and the main evaluation loop.
// It blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	// Log Polymarket wallet for debugging.
	polyAddr := e.clobClient.PolymarketAddress()
	slog.Info("polymarket wallet", "poly", strings.ToLower(polyAddr.Hex()), "eoa", strings.ToLower(e.clobClient.Address().Hex()))

	// Attempt fast restart: restore candle history from DB if the gap is short.
	e.tryFastRestart()

	// Resolve initial markets and start WS.
	if err := e.rotateMarket(ctx); err != nil {
		return fmt.Errorf("initial market resolve: %w", err)
	}

	// Restore slot state from DB: if a slot already traded in the current
	// 5m window (before a crash/restart), mark it as filled to prevent
	// duplicate orders. Also sync API positions to slot position tracking.
	e.restoreSlotState()

	// Start price feed.
	// When polymarket_oracle=true, use Polymarket RTDS Chainlink as the sole
	// data source — the exact oracle used for 5m Up/Down resolution.
	// All metrics (price, trend, candles) are derived from the same source
	// that determines settlement, eliminating any data source mismatch.
	// Binance is only used as a fallback when polymarket_oracle is false.
	var priceTicks <-chan feed.PriceTick

	if e.cfg.Feed.PolymarketOracle {
		rf := feed.NewRTDSFeed("btc/usd")
		go rf.Run(ctx)
		priceTicks = rf.Ticks()
		slog.Info("price feed started", "type", "polymarket-rtds", "symbol", "btc/usd")
	} else if e.cfg.Feed.BinanceSymbol != "" {
		// Seed initial BTC price from Binance REST API so we don't start at $0.
		if price, err := feed.FetchSpotPrice(ctx, e.cfg.Feed.BinanceSymbol); err != nil {
			slog.Warn("binance seed price failed", "err", err)
		} else {
			e.btcPrice = price
			now := time.Now()
			e.lastTickServerTime = now
			e.trend.Add(price, now)
			e.candles.AddTick(price, now)
			slog.Info("seed price", "symbol", e.cfg.Feed.BinanceSymbol, "price", price)
		}
		bf := feed.NewBinanceFeed(
			e.cfg.Feed.BinanceSymbol,
			e.cfg.Feed.PollInterval(),
		)
		go bf.Run(ctx)
		priceTicks = bf.Ticks()
		slog.Info("price feed started", "type", "binance", "symbol", e.cfg.Feed.BinanceSymbol)
	} else if e.cfg.Feed.ChainlinkRPC != "" {
		clf := feed.NewChainlinkFeed(
			e.cfg.Feed.ChainlinkRPC,
			e.cfg.Feed.ChainlinkContract,
			e.cfg.Feed.PollInterval(),
		)
		go clf.Run(ctx)
		priceTicks = clf.Ticks()
		slog.Info("price feed started", "type", "chainlink", "contract", e.cfg.Feed.ChainlinkContract)
	}

	slog.Info("engine started", "strategy", e.paperSlots[0].strategy.Name(), "slots", len(e.paperSlots), "dynamic", e.isDynamic)

	// Start HTTP server for live reports.
	e.startReportServer()

	// Start background goroutines with WaitGroup for graceful shutdown.
	var bgWg sync.WaitGroup
	bgWg.Add(2)
	go func() { defer bgWg.Done(); e.sellLoop(ctx) }()
	go func() { defer bgWg.Done(); e.claimLoop(ctx) }()

	// Market rotation ticker: check every 30 seconds for 5-minute window boundary.
	rotationTicker := time.NewTicker(30 * time.Second)
	defer rotationTicker.Stop()

	// Main select loop: strategy evaluation is event-driven.
	// WS events and price ticks update state and trigger evaluation immediately.
	for {
		select {
		case <-ctx.Done():
			slog.Info("engine shutting down, waiting for background goroutines")
			bgWg.Wait()
			slog.Info("background goroutines stopped")
			if e.cfg.DryRun {
				for _, slot := range e.paperSlots {
					slot.paper.Report()
				}
			}
			e.collector.Report()
			if e.store != nil {
				e.store.Close()
			}
			return nil

		case <-e.wsNotify():
			// SnapshotStore already updated by ws.Client; just evaluate.
			e.evaluate(ctx)

		case evt := <-e.wsResolved():
			e.handleMarketResolved(evt)

		case tick := <-priceTicks:
			e.btcPrice = tick.Price
			e.lastTickTime = time.Now()
			e.lastTickServerTime = tick.Time
			e.trend.Add(tick.Price, tick.Time)
			e.candles.AddTick(tick.Price, tick.Time)
			if e.resolveCompletedWindow() && e.isDynamic {
				if err := e.rotateMarket(ctx); err != nil {
					slog.Error("market rotation error (window boundary)", "err", err)
				}
			}
			e.evaluate(ctx)

		case <-rotationTicker.C:
			if e.isDynamic {
				if err := e.rotateMarket(ctx); err != nil {
					slog.Error("market rotation error", "err", err)
				}
			} else {
				e.refreshPositions(ctx)
			}
		}
	}
}

// wsNotify returns the WS notify channel, or nil if no client is active.
func (e *Engine) wsNotify() <-chan struct{} {
	if e.wsClient == nil {
		return nil
	}
	return e.wsClient.Notify()
}

// wsResolved returns the WS resolved channel, or nil if no client is active.
func (e *Engine) wsResolved() <-chan ws.MarketEvent {
	if e.wsClient == nil {
		return nil
	}
	return e.wsClient.Resolved()
}

// rotateMarket resolves the current slug and, if it changed, re-subscribes WS.
func (e *Engine) rotateMarket(ctx context.Context) error {
	slug, err := config.ResolveSlug(e.slugTemplate, e.cfg.Market.Timezone)
	if err != nil {
		return fmt.Errorf("resolve slug: %w", err)
	}

	// No change — skip rotation.
	if slug == e.currentSlug && len(e.markets) > 0 {
		return nil
	}

	slog.Info("market rotation", "from", e.currentSlug, "to", slug, "now", time.Now().UTC().Format("15:04:05"))
	e.currentSlug = slug
	e.cfg.Market.EventSlug = slug

	// Stop previous WS client.
	if e.wsClient != nil {
		e.wsClient.Stop()
		e.wsClient = nil
	}

	// Save previous market tokens so the new WS can still receive
	// market_resolved events for the just-completed window.
	e.prevMarketTokens = nil
	for _, m := range e.markets {
		e.prevMarketTokens = append(e.prevMarketTokens, m.yesTokenID, m.noTokenID)
	}

	// Clear previous market state.
	e.markets = nil
	e.snapshots.Reset()
	e.lastSignal = strategy.Hold
	e.lastOrderedToken = ""

	// Resolve new markets.
	if err := e.resolveMarkets(ctx); err != nil {
		return fmt.Errorf("resolve markets for %s: %w", slug, err)
	}

	// Collect token IDs for the new WS subscription.
	// Include previous window's tokens so we receive the market_resolved
	// event that confirms the official resolution direction.
	var assetIDs []string
	for _, m := range e.markets {
		assetIDs = append(assetIDs, m.yesTokenID, m.noTokenID)
	}
	assetIDs = append(assetIDs, e.prevMarketTokens...)
	e.wsClient = ws.NewClient(assetIDs, e.snapshots)
	go e.wsClient.Run(ctx)

	// Refresh positions for new markets.
	e.refreshPositions(ctx)

	slog.Info("market rotated", "slug", slug, "markets", len(e.markets), "assets", len(assetIDs))
	return nil
}

// resolveMarkets fetches the event and resolves all markets with their range bounds.
func (e *Engine) resolveMarkets(ctx context.Context) error {
	gammaClient := gamma.NewClient()

	event, err := gammaClient.GetEventBySlug(ctx, e.cfg.Market.EventSlug)
	if err != nil {
		return fmt.Errorf("get event: %w", err)
	}
	if len(event.Markets) == 0 {
		return fmt.Errorf("event %q has no markets", e.cfg.Market.EventSlug)
	}

	slog.Info("event resolved", "id", event.ID, "title", event.Title, "markets", len(event.Markets))

	// Fetch tick_size and neg_risk once from the first market (shared within neg-risk events).
	firstYes, _, err := parseClobTokenIDs(event.Markets[0].ClobTokenIDs)
	if err != nil {
		return fmt.Errorf("parse first market tokens: %w", err)
	}
	tickSize, err := e.clobClient.GetTickSize(ctx, firstYes)
	if err != nil {
		return fmt.Errorf("get tick size: %w", err)
	}
	negRisk, err := e.clobClient.GetNegRisk(ctx, firstYes)
	if err != nil {
		return fmt.Errorf("get neg risk: %w", err)
	}
	feeRateBps, err := e.clobClient.GetFeeRate(ctx, firstYes)
	if err != nil {
		slog.Warn("get fee rate failed, defaulting to 0", "err", err)
		feeRateBps = 0
	}
	slog.Info("market params", "tick_size", tickSize, "neg_risk", negRisk, "fee_rate_bps", feeRateBps)

	for _, gm := range event.Markets {
		slog.Info("market time boundaries",
			"start_date", gm.StartDate, "end_date", gm.EndDate,
			"start_date_iso", gm.StartDateIso, "end_date_iso", gm.EndDateIso)

		yesToken, noToken, err := parseClobTokenIDs(gm.ClobTokenIDs)
		if err != nil {
			slog.Warn("skip market", "id", gm.ID, "err", err)
			continue
		}

		lower, upper := parseRangeFromQuestion(gm.Question)

		meta := marketMeta{
			question:    gm.Question,
			conditionID: gm.ConditionID,
			yesTokenID:  yesToken,
			noTokenID:   noToken,
			tickSize:    tickSize,
			negRisk:     negRisk,
			feeRateBps:  feeRateBps,
			lowerBound:  lower,
			upperBound:  upper,
		}
		e.markets = append(e.markets, meta)

		rangeStr := formatRange(lower, upper)
		slog.Info("market resolved", "question", gm.Question, "range", rangeStr, "yes_token", truncID(yesToken))
	}

	if len(e.markets) == 0 {
		return fmt.Errorf("no valid markets resolved")
	}
	return nil
}

// buildCandleState constructs the candle direction summary for strategy evaluation.
func (e *Engine) buildCandleState() strategy.CandleState {
	cs := strategy.CandleState{
		Current5m:  string(e.candles.CurrentDirection(5 * time.Minute)),
		Current15m: string(e.candles.CurrentDirection(15 * time.Minute)),
	}

	if c, ok := e.candles.CurrentCandle(5 * time.Minute); ok {
		cs.Change5m = c.Close - c.Open
		cs.Partial5m = c.Partial
		// Compute window timing for end-of-window trading.
		// Use server-side tick timestamp instead of local clock so that
		// two machines receiving the same feed produce identical elapsed
		// values regardless of system clock drift.
		now := e.lastTickServerTime
		if now.IsZero() {
			now = time.Now() // fallback before first tick arrives
		}
		elapsed := now.Sub(c.Start)
		if elapsed < 0 {
			elapsed = 0
		}
		if elapsed > 5*time.Minute {
			elapsed = 5 * time.Minute
		}
		cs.Elapsed5m = elapsed
		cs.Remaining5m = 5*time.Minute - elapsed
	}
	if c, ok := e.candles.CurrentCandle(15 * time.Minute); ok {
		cs.Change15m = c.Close - c.Open
	}
	if c, ok := e.candles.LastCompleted(5 * time.Minute); ok {
		cs.Last5m = string(c.Direction)
	}
	if c, ok := e.candles.LastCompleted(15 * time.Minute); ok {
		cs.Last15m = string(c.Direction)
	}

	// Populate recent completed 5m directions (newest first) for reversal detection.
	completed := e.candles.CompletedCandles(5 * time.Minute)
	if len(completed) > 0 {
		dirs := make([]string, len(completed))
		for i, c := range completed {
			dirs[len(completed)-1-i] = string(c.Direction)
		}
		cs.RecentDirs5m = dirs
	}

	return cs
}

// evaluate builds the full market state and runs all slots concurrently.
// Each slot independently evaluates its strategy and executes (paper or live).
func (e *Engine) evaluate(ctx context.Context) {
	// Grow pre-allocated buffer if needed (only on market rotation).
	if cap(e.evalMarkets) < len(e.markets) {
		e.evalMarkets = make([]strategy.MarketInfo, len(e.markets))
	}
	e.evalMarkets = e.evalMarkets[:len(e.markets)]

	state := strategy.MarketState{
		BTCSpotPrice: e.btcPrice,
		Trend:        e.trend.Compute(),
		Candles:      e.buildCandleState(),
		Markets:      e.evalMarkets,
	}

	// Skip evaluation until WS data arrives for all tokens.
	for _, m := range e.markets {
		if !e.snapshots.Has(m.yesTokenID) || !e.snapshots.Has(m.noTokenID) {
			return
		}
	}

	for i, m := range e.markets {
		yesSnap := e.snapshots.Get(m.yesTokenID)
		noSnap := e.snapshots.Get(m.noTokenID)
		state.Markets[i] = strategy.MarketInfo{
			Question:   m.question,
			YesTokenID: m.yesTokenID,
			NoTokenID:  m.noTokenID,
			TickSize:   m.tickSize,
			NegRisk:    m.negRisk,
			FeeRateBps: m.feeRateBps,
			LowerBound: m.lowerBound,
			UpperBound: m.upperBound,
			BestBid:    parseFloat(yesSnap.BestBid),
			BestAsk:    parseFloat(yesSnap.BestAsk),
			NoBestBid:  parseFloat(noSnap.BestBid),
			NoBestAsk:  parseFloat(noSnap.BestAsk),
		}
	}

	// Collect market data for backtesting analysis.
	if windowStart := e.currentWindowStart(); !windowStart.IsZero() && len(state.Markets) > 0 {
		m := state.Markets[0]
		btcOpen := 0.0
		if c, ok := e.candles.CurrentCandle(5 * time.Minute); ok {
			btcOpen = c.Open
		}
		e.collector.Sample(windowStart, e.btcPrice, btcOpen,
			m.BestAsk, m.BestBid, m.NoBestAsk, m.NoBestBid)
	}

	// Dashboard log: use first slot's signal for the summary line.
	// Always runs even when price is stale, so tick_age remains visible.
	e.logDashboard(state)

	// Freshness guard: skip strategy execution if price data is stale.
	// Normal tick rate is ~1/s; >1s means feed is likely interrupted.
	// Placed after logDashboard so tick_age remains observable during outages.
	const maxTickAge = 1 * time.Second
	if !e.lastTickTime.IsZero() && time.Since(e.lastTickTime) > maxTickAge {
		return
	}

	// Evaluate all slots concurrently (each with its own entry price).
	e.evaluateSlots(ctx, state)
}

// logDashboard emits structured dashboard log lines.
//
// Uses event-driven logging for signal transitions and periodic heartbeat
// for market state — standard practice for quantitative trading systems.
//
// Log events:
//   - On BUY transition: emits signal event with side/price/cost/reason
//   - Every dashboardTickInterval: emits tick heartbeat with market state
//
// Bot states:
//   - scanning: evaluating market, no position in current window
//   - filled:   position taken in current window, waiting for resolution
func (e *Engine) logDashboard(state strategy.MarketState) {
	sig := e.paperSlots[0].strategy.Evaluate(state)

	slot := e.paperSlots[0]
	windowStart := e.currentWindowStart()
	windowFilled := !windowStart.IsZero() && windowStart.Equal(slot.lastBuyWindow)

	// BUY in a filled window is effectively HOLD for dashboard purposes.
	effectiveAction := sig.Action
	if sig.Action == strategy.Buy && windowFilled {
		effectiveAction = strategy.Hold
	}

	now := time.Now()
	transition := effectiveAction != e.lastSignal ||
		(effectiveAction != strategy.Hold && sig.TokenID != e.lastOrderedToken)
	heartbeat := now.Sub(e.lastLogTime) >= dashboardTickInterval

	if transition || heartbeat {
		e.emitDashboard(state, sig, effectiveAction, windowFilled, transition)
		e.emitSlotSummary(state)
		e.lastLogTime = now
	}

	e.lastSignal = effectiveAction
	if effectiveAction != strategy.Hold {
		e.lastOrderedToken = sig.TokenID
	} else {
		e.lastOrderedToken = ""
	}
}

// emitDashboard formats and writes a single structured dashboard log line.
func (e *Engine) emitDashboard(
	state strategy.MarketState, sig strategy.Signal,
	effective strategy.Action, filled, transition bool,
) {
	stratName := e.paperSlots[0].strategy.Name()
	switch stratName {
	case "spread":
		e.emitDashboardSpread(state, sig, effective, filled, transition)
	default:
		e.emitDashboardBTCUpDown(state, sig, effective, filled, transition)
	}
}

// emitDashboardSpread emits a compact tick log for the spread strategy.
// Only shows fields relevant to spread: btc, remain, yes_ask, no_ask, spread, state.
func (e *Engine) emitDashboardSpread(
	state strategy.MarketState, sig strategy.Signal,
	effective strategy.Action, filled, transition bool,
) {
	cs := state.Candles

	args := make([]any, 0, 16)
	args = append(args, "btc", fmt.Sprintf("%.0f", e.btcPrice))
	if !e.lastTickTime.IsZero() {
		args = append(args, "tick_age", fmt.Sprintf("%.1fs", time.Since(e.lastTickTime).Seconds()))
	}

	// Window timing.
	if cs.Current5m != "Unknown" {
		args = append(args, "remain", fmt.Sprintf("%ds", int(cs.Remaining5m.Seconds())))
	}

	// Market prices and spread.
	if len(state.Markets) > 0 {
		m := state.Markets[0]
		if m.BestAsk > 0 || m.NoBestAsk > 0 {
			args = append(args,
				"yes_ask", fmt.Sprintf("%.2f", m.BestAsk),
				"no_ask", fmt.Sprintf("%.2f", m.NoBestAsk),
				"spread", fmt.Sprintf("%.2f", math.Abs(m.BestAsk-m.NoBestAsk)),
			)
		}
	}

	// Result: signal event or tick heartbeat with hold reason.
	if effective == strategy.Buy && transition {
		args = append(args,
			"signal", "BUY",
			"side", e.paperSide(sig.TokenID),
			"price", fmt.Sprintf("%.2f", sig.Price),
			"cost", fmt.Sprintf("%.2f", sig.MaxCost),
			"reason", sig.Reason,
		)
	} else {
		botState := "scanning"
		if filled {
			botState = "filled"
		}
		args = append(args, "state", botState)
		if !filled && sig.Reason != "" {
			args = append(args, "hold", briefHoldCode(sig.Reason))
		}
	}

	slog.Info("tick", args...)
}

// emitDashboardBTCUpDown emits the full tick log for the btc_updown strategy.
func (e *Engine) emitDashboardBTCUpDown(
	state strategy.MarketState, sig strategy.Signal,
	effective strategy.Action, filled, transition bool,
) {
	trend := state.Trend
	cs := state.Candles

	// Message is a static event label; all data goes into structured fields.
	msg := "tick"

	// Structured fields grouped for at-a-glance reading:
	//   1. BTC price
	//   2. Candle changes: c5m, c15m (same family, adjacent)
	//   3. Decision comparison: threshold, strength (|c5m|/threshold)
	//   4. Trend context: trend_1m, trend_5m, vol
	//   5. Window timing: elapsed, remain, last_5m
	//   6. Market prices: yes_ask, no_ask, limit
	//   7. Result: state/signal, hold reason
	args := make([]any, 0, 28)
	args = append(args, "btc", fmt.Sprintf("%.0f", e.btcPrice))
	if !e.lastTickTime.IsZero() {
		args = append(args, "tick_age", fmt.Sprintf("%.1fs", time.Since(e.lastTickTime).Seconds()))
	}

	// Candle changes: 5m and 15m together for multi-timeframe comparison.
	if cs.Current5m != "Unknown" {
		args = append(args, "c5m", fmt.Sprintf("%+.0f", cs.Change5m))
		if cs.Current15m != "Unknown" {
			args = append(args, "c15m", fmt.Sprintf("%+.0f", cs.Change15m))
		}
	}

	// Decision comparison: threshold, strength, and z-score.
	// strength = |c5m| / threshold — >=1.0 means candle exceeds threshold.
	// z = |c5m| / (vol * sqrt(elapsed)) — raw z-score (volatility-normalized).
	if sig.Threshold > 0 {
		args = append(args, "threshold", fmt.Sprintf("%.0f", sig.Threshold))
		if cs.Current5m != "Unknown" {
			strength := math.Abs(cs.Change5m) / sig.Threshold
			args = append(args, "strength", fmt.Sprintf("%.1f", strength))
		}
	}
	if cs.Current5m != "Unknown" && trend.Volatility > 0 && cs.Elapsed5m > 0 {
		z := math.Abs(cs.Change5m) / (trend.Volatility * math.Sqrt(cs.Elapsed5m.Seconds()))
		args = append(args, "z", fmt.Sprintf("%.1f", z))
	}

	// Trend context: rolling change over 1m/5m (continuous, no reset).
	if trend.IsReady() {
		accel := trend.Speed1m - trend.Speed5m
		args = append(args,
			"trend_1m", fmt.Sprintf("%+.0f", trend.Change1m),
			"trend_5m", fmt.Sprintf("%+.0f", trend.Change5m),
			"vol", fmt.Sprintf("%.1f", trend.Volatility),
			"s1m", fmt.Sprintf("%+.0f", trend.Speed1m),
			"s5m", fmt.Sprintf("%+.0f", trend.Speed5m),
			"accel", fmt.Sprintf("%+.0f", accel),
		)
	}

	// Window timing: elapsed, remaining.
	if cs.Current5m != "Unknown" {
		args = append(args,
			"elapsed", fmt.Sprintf("%ds", int(cs.Elapsed5m.Seconds())),
			"remain", fmt.Sprintf("%ds", int(cs.Remaining5m.Seconds())),
		)
		if cs.Last5m != "" {
			args = append(args, "last_5m", cs.Last5m)
		}
	}

	// Market prices: yes/no asks and entry limit for price range comparison.
	if len(state.Markets) > 0 {
		m := state.Markets[0]
		if m.BestAsk > 0 || m.NoBestAsk > 0 {
			args = append(args,
				"yes_ask", fmt.Sprintf("%.2f", m.BestAsk),
				"no_ask", fmt.Sprintf("%.2f", m.NoBestAsk),
			)
		}
	}
	if sig.EntryLimit > 0 {
		args = append(args, "limit", fmt.Sprintf("%.2f", sig.EntryLimit))
	}

	// Result: signal event or tick heartbeat with hold reason.
	if effective == strategy.Buy && transition {
		args = append(args,
			"signal", "BUY",
			"side", e.paperSide(sig.TokenID),
			"price", fmt.Sprintf("%.2f", sig.Price),
			"cost", fmt.Sprintf("%.2f", sig.MaxCost),
			"reason", sig.Reason,
		)
	} else {
		botState := "scanning"
		if filled {
			botState = "filled"
		}
		args = append(args, "state", botState)
		if !filled && sig.Reason != "" {
			args = append(args, "hold", briefHoldCode(sig.Reason))
		}
	}

	slog.Info(msg, args...)
}

// evaluateSlots runs strategy evaluation independently for each slot.
// briefHoldCode extracts the short code before ":" from a structured reason string.
// e.g., "no_trend: candle=-98 ..." → "no_trend"
func briefHoldCode(reason string) string {
	if i := strings.Index(reason, ":"); i > 0 && i < 30 {
		return reason[:i]
	}
	return reason
}

// slotSummaryCode maps briefHoldCode output to compact keys for the slots summary.
var slotSummaryCode = map[string]string{
	"too_young":      "young",
	"momentum_decay": "decay",
	"partial_candle": "partial",
	"late_weak":      "late_w",
	"late_no_fill":   "late_nf",
	"late_no_trend":  "late_nt",
	"weak_signal":    "weak",
}

// emitSlotSummary evaluates all paper slots and emits a single summary log line.
// Called from logDashboard on the same heartbeat cadence to show per-slot status.
func (e *Engine) emitSlotSummary(state strategy.MarketState) {
	windowStart := e.currentWindowStart()
	counts := make(map[string]int)
	var buySlots []string

	for _, slot := range e.paperSlots {
		// Build per-slot market state with the slot's own positions.
		if cap(slot.marketsBuf) < len(state.Markets) {
			slot.marketsBuf = make([]strategy.MarketInfo, len(state.Markets))
		}
		slot.marketsBuf = slot.marketsBuf[:len(state.Markets)]
		copy(slot.marketsBuf, state.Markets)
		slotState := state
		slotState.Markets = slot.marketsBuf
		for i := range slotState.Markets {
			m := &slotState.Markets[i]
			m.YesShares = slot.positions[m.YesTokenID]
			m.NoShares = slot.positions[m.NoTokenID]
			if ep, ok := slot.entryPrices[m.YesTokenID]; ok {
				m.EntryPrice = ep
				m.EntryElapsed = slot.entryElapsed[m.YesTokenID]
			} else if ep, ok := slot.entryPrices[m.NoTokenID]; ok {
				m.EntryPrice = ep
				m.EntryElapsed = slot.entryElapsed[m.NoTokenID]
			}
		}

		sig := slot.strategy.Evaluate(slotState)

		// A BUY in a filled window is effectively HOLD.
		windowFilled := !windowStart.IsZero() && windowStart.Equal(slot.lastBuyWindow)
		if sig.Action == strategy.Buy && windowFilled {
			sig.Action = strategy.Hold
			sig.Reason = "holding"
		}

		if sig.Action == strategy.Buy {
			buySlots = append(buySlots, fmt.Sprintf("$%.2f", slot.entryPrice))
			counts["buy"]++
		} else if sig.Action == strategy.Sell {
			counts["exit"]++
		} else {
			code := briefHoldCode(sig.Reason)
			if short, ok := slotSummaryCode[code]; ok {
				code = short
			}
			counts[code]++
		}
	}

	// Build structured log args: total + each non-zero reason count.
	args := []any{"total", len(e.paperSlots)}
	// Emit reason counts in a stable order for readability.
	for _, key := range []string{"young", "no_trend", "no_fill", "decay", "partial", "holding", "exit", "no_data", "no_market", "trend_warmup", "late_w", "late_nf", "late_nt", "weak"} {
		if n := counts[key]; n > 0 {
			args = append(args, key, n)
		}
	}
	// Append any remaining keys not in the predefined order.
	for key, n := range counts {
		switch key {
		case "young", "no_trend", "no_fill", "decay", "partial", "holding", "exit", "no_data", "no_market", "trend_warmup", "buy", "late_w", "late_nf", "late_nt", "weak":
			continue
		}
		if n > 0 {
			args = append(args, key, n)
		}
	}
	if buyCount := counts["buy"]; buyCount > 0 {
		args = append(args, "buy", buyCount)
		// Sort buy slots by entry price descending.
		sort.Slice(buySlots, func(i, j int) bool {
			return buySlots[i] > buySlots[j]
		})
		args = append(args, "buy_slots", strings.Join(buySlots, ","))
	}
	slog.Info("slots", args...)
}

// Pipeline: strategy.Evaluate → RiskManager.PreCheck → RiskManager.PreTrade → OrderManager.Execute.
// In dry-run mode: records to PaperTrader. In live mode: executes real orders.
// Each slot has its own entry price, position tracking, and buy dedup.
func (e *Engine) evaluateSlots(ctx context.Context, state strategy.MarketState) {
	windowStart := e.currentWindowStart()

	for _, slot := range e.paperSlots {
		log := slog.With("slot", slot.paper.label)
		// 1. Build slot-specific market state with the slot's own positions.
		if cap(slot.marketsBuf) < len(state.Markets) {
			slot.marketsBuf = make([]strategy.MarketInfo, len(state.Markets))
		}
		slot.marketsBuf = slot.marketsBuf[:len(state.Markets)]
		copy(slot.marketsBuf, state.Markets)
		slotState := state
		slotState.Markets = slot.marketsBuf
		for i := range slotState.Markets {
			m := &slotState.Markets[i]
			m.YesShares = slot.positions[m.YesTokenID]
			m.NoShares = slot.positions[m.NoTokenID]
			if ep, ok := slot.entryPrices[m.YesTokenID]; ok {
				m.EntryPrice = ep
				m.EntryElapsed = slot.entryElapsed[m.YesTokenID]
			} else if ep, ok := slot.entryPrices[m.NoTokenID]; ok {
				m.EntryPrice = ep
				m.EntryElapsed = slot.entryElapsed[m.NoTokenID]
			}
		}

		// 2. Strategy: pure signal generation.
		sig := slot.strategy.Evaluate(slotState)

		// 3. Risk: pre-check (window dedup, signal dedup).
		sig, verdict := e.riskMgr.PreCheck(sig, slot, windowStart)
		if verdict == VerdictHold {
			slot.paper.RecordHold(sig.Reason)
			slot.lastSignal = strategy.Hold
			slot.lastOrderedToken = ""
			continue
		}
		if verdict == VerdictDedup {
			continue
		}

		// 4. Execution path: live orders vs paper recording.
		if !e.cfg.DryRun && slot.live {
			// 4a. FAK retry guard: skip early if max retries exhausted or in cooldown.
			now := time.Now()
			if sig.Action == strategy.Sell {
				// Sell: apply cooldown with a higher retry cap; fallback to relayer redeem when exhausted.
				if slot.windowRetries >= maxEarlyExitRetries {
					log.Warn("early exit retries exhausted, falling back to relayer redeem",
						"retries", slot.windowRetries)
					e.enqueueEarlyExitSell(slot, sig)
					// Clear position to stop re-signaling.
					delete(slot.positions, sig.TokenID)
					delete(slot.entryPrices, sig.TokenID)
					delete(slot.entryElapsed, sig.TokenID)
					if !windowStart.IsZero() {
						slot.lastBuyWindow = windowStart
					}
					slot.windowRetries = 0
					continue
				}
				if slot.windowRetries > 0 && now.Sub(slot.lastFailedAt) < e.cfg.Execution.RetryCooldown() {
					continue // still in cooldown after last failure
				}
			} else {
				// Buy: existing retry guard logic.
				if slot.windowRetries >= e.cfg.Execution.MaxWindowRetries {
					// Mark window as done so PreCheck deduplicates future evaluations silently.
					if !windowStart.IsZero() {
						slot.lastBuyWindow = windowStart
					}
					continue
				}
				if slot.windowRetries > 0 && now.Sub(slot.lastFailedAt) < e.cfg.Execution.RetryCooldown() {
					continue // still in cooldown after last failure
				}
			}

			// 4b. Risk: pre-trade (circuit breaker).
			if e.riskMgr.PreTrade(sig) != VerdictPass {
				continue
			}

			// Log live slot signal only when actually attempting to execute.
			mktPrice := MarketPrice(e.snapshots, sig.TokenID, sig.Side)

			// Step down sell price on retries to sweep deeper into the order book.
			// When bid-side has no liquidity at best bid, progressively lower the
			// price to match against orders further from the top of book.
			if sig.Action == strategy.Sell && slot.windowRetries > 0 {
				tickSize := parseFloat(sig.TickSize)
				if tickSize > 0 {
					stepDown := float64(slot.windowRetries) * tickSize
					mktPrice -= stepDown
					sig.Price -= stepDown
					// Floor at one tick — never go to zero or negative.
					if mktPrice < tickSize {
						mktPrice = tickSize
					}
					if sig.Price < tickSize {
						sig.Price = tickSize
					}
					log.Debug("sell price step-down",
						"retry", slot.windowRetries,
						"step", fmt.Sprintf("%.4f", stepDown),
						"exec_price", fmt.Sprintf("%.4f", mktPrice))
				}
			}

			log.Info("signal",
				"side", sig.Side,
				"price", fmt.Sprintf("%.4f", sig.Price),
				"cost", fmt.Sprintf("%.2f", sig.MaxCost),
				"reason", sig.Reason)

			// 4c. Order: execute via exchange.
			result := e.orderMgr.Execute(ctx, sig, slot.paper.label, mktPrice)

			if result.Success {
				// Only count buys toward daily risk limits; sells recover capital.
				if sig.Action == strategy.Buy {
					tradeCost := sig.MaxCost
					if tradeCost <= 0 {
						tradeCost = sig.Price * sig.Size
					}
					e.riskMgr.RecordTrade(tradeCost)
				}
				e.recordSlotTrade(slot, sig, state.Candles, windowStart)
				slot.windowRetries = 0 // reset on success
			} else {
				// Order failed: allow retry on next evaluation tick.
				// Do NOT set lastSignal/lastOrderedToken so signal is not deduped.
				slot.windowRetries++
				slot.lastFailedAt = now
				retryLimit := e.cfg.Execution.MaxWindowRetries
				if sig.Action == strategy.Sell {
					retryLimit = maxEarlyExitRetries
				}
				logArgs := []any{
					"attempt", fmt.Sprintf("%d/%d", slot.windowRetries, retryLimit),
					"cooldown", e.cfg.Execution.RetryCooldown(),
				}
				if sig.Action == strategy.Sell {
					snap := e.snapshots.Get(sig.TokenID)
					logArgs = append(logArgs, "bid", snap.BestBid, "ask", snap.BestAsk)
				}
				log.Warn("order retry", logArgs...)
			}
		} else {
			// 4c. Paper-only or dry-run: simulate realistic execution.
			// Use current market price from WS snapshot (same as live path).
			mktPrice := MarketPrice(e.snapshots, sig.TokenID, sig.Side)
			if mktPrice <= 0 {
				// No market price — a real FAK order would also fail.
				log.Debug("paper skip: no market price",
					"token", truncID(sig.TokenID))
				continue
			}

			// Slippage check: same threshold as live OrderManager.
			if sig.Price > 0 {
				deviation := math.Abs(mktPrice-sig.Price) / sig.Price
				if deviation > e.cfg.Execution.MaxSlippagePct {
					log.Debug("paper skip: slippage exceeded",
						"signal_price", sig.Price, "market_price", mktPrice,
						"deviation_pct", fmt.Sprintf("%.2f%%", deviation*100))
					continue
				}
			}

			// Entry limit check: reject if current ask exceeds the
			// strategy's price ceiling. A real order would not fill above
			// this limit, so the paper trade should not either.
			if sig.EntryLimit > 0 && mktPrice > sig.EntryLimit {
				log.Debug("paper skip: ask exceeds entry limit",
					"slot", slot.paper.Label(),
					"ask", fmt.Sprintf("%.4f", mktPrice),
					"entry_limit", fmt.Sprintf("%.4f", sig.EntryLimit))
				continue
			}

			// Record with actual market price, recalculate size.
			sig.Price = mktPrice
			if sig.MaxCost > 0 && mktPrice > 0 {
				sig.Size = sig.MaxCost / mktPrice
			}
			e.recordSlotTrade(slot, sig, state.Candles, windowStart)
		}
	}
}

// recordSlotTrade updates slot state and paper trading records after a trade.
func (e *Engine) recordSlotTrade(slot *paperSlot, sig strategy.Signal, candles strategy.CandleState, windowStart time.Time) {
	if sig.Action == strategy.Buy {
		side := e.paperSide(sig.TokenID)
		if !windowStart.IsZero() {
			slot.paper.RecordBuy(windowStart, side, sig.Price, sig.Size, candles.Remaining5m, candles.Change5m)
			// Persist to DB.
			if e.store != nil {
				if err := e.store.InsertPaperTrade(store.PaperTradeRow{
					SlotLabel:   slot.paper.Label(),
					EntryTime:   time.Now(),
					WindowStart: windowStart,
					Side:        side,
					BuyPrice:    sig.Price,
					Size:        sig.Size,
					RemainingNs: int64(candles.Remaining5m),
					Change5m:    candles.Change5m,
					Live:        slot.live,
				}); err != nil {
					slog.Warn("failed to persist paper trade", "slot", slot.paper.Label(), "err", err)
				}
			}
		}
		slot.positions[sig.TokenID] = sig.Size
		// Track entry price and elapsed for early exit stop-loss.
		slot.entryPrices[sig.TokenID] = sig.Price
		slot.entryElapsed[sig.TokenID] = candles.Elapsed5m
	}
	if sig.Action == strategy.Sell {
		// Early exit: record before clearing state.
		entryPrice := slot.entryPrices[sig.TokenID]
		if entryPrice > 0 {
			side := e.paperSide(sig.TokenID)
			slot.paper.RecordEarlyExit(windowStart, side, entryPrice, sig.Price, sig.Size)
		}
		delete(slot.positions, sig.TokenID)
		delete(slot.entryPrices, sig.TokenID)
		delete(slot.entryElapsed, sig.TokenID)
		// Prevent same-window re-entry.
		if !windowStart.IsZero() {
			slot.lastBuyWindow = windowStart
		}
	}
	slot.lastSignal = sig.Action
	slot.lastOrderedToken = sig.TokenID
	if sig.Action == strategy.Buy && !windowStart.IsZero() {
		slot.lastBuyWindow = windowStart
	}
	e.notifyDashboard()
}

// notifyDashboard pushes an update to all connected SSE clients.
func (e *Engine) notifyDashboard() {
	if e.paperHandler != nil {
		e.paperHandler.Notify()
	}
}

const dataAPIBaseURL = "https://data-api.polymarket.com"

// dataPosition is a single position from the Polymarket Data API.
type dataPosition struct {
	Asset        string  `json:"asset"` // token ID
	ConditionID  string  `json:"conditionId"`
	OutcomeIndex int     `json:"outcomeIndex"` // 0 = Yes, 1 = No
	Size         float64 `json:"size"`
	CurPrice     float64 `json:"curPrice"`
	CashPnl      float64 `json:"cashPnl"`
	Title        string  `json:"title"`
	Outcome      string  `json:"outcome"`
	EventSlug    string  `json:"eventSlug"`
	Redeemable   bool    `json:"redeemable"`
	NegativeRisk bool    `json:"negativeRisk"`
	EndDate      string  `json:"endDate"`
}

// refreshPositions fetches all positions from the Polymarket Data API
// in a single request and updates the local position cache.
// It queries both the funder (proxy wallet) and signer addresses.
func (e *Engine) refreshPositions(ctx context.Context) {
	// Build a set of tracked token IDs for fast lookup.
	tracked := make(map[string]*marketMeta, len(e.markets)*2)
	for i := range e.markets {
		tracked[e.markets[i].yesTokenID] = &e.markets[i]
		tracked[e.markets[i].noTokenID] = &e.markets[i]
	}

	// Polymarket wallet address (configured or derived from EOA).
	// Positions are held in this wallet, not the EOA directly.
	polyAddr := e.clobClient.PolymarketAddress()
	addrs := []string{strings.ToLower(polyAddr.Hex())}

	// Also try funder if explicitly configured and different.
	funder := strings.ToLower(e.clobClient.Funder().Hex())
	if funder != addrs[0] {
		addrs = append(addrs, funder)
	}

	e.posMu.Lock()
	clear(e.positions)
	for _, addr := range addrs {
		positions := e.fetchPositions(ctx, addr)
		if positions == nil {
			continue
		}
		slog.Debug("positions fetched", "addr", addr, "total", len(positions))
		for _, p := range positions {
			if p.Size <= 0 {
				continue
			}
			if m, ok := tracked[p.Asset]; ok {
				e.positions[p.Asset] = p.Size
				side := "YES"
				if p.Asset == m.noTokenID {
					side = "NO"
				}
				slog.Debug("position tracked", "range", formatRange(m.lowerBound, m.upperBound), "side", side, "shares", p.Size, "price", p.CurPrice, "pnl", p.CashPnl)
			}
		}
	}

	if len(e.positions) == 0 {
		slog.Debug("no tracked positions found")
	}
	e.posMu.Unlock()
}

// dataHTTPClient is a dedicated HTTP client for the Polymarket Data API with timeout.
var dataHTTPClient = &http.Client{Timeout: 30 * time.Second}

// fetchPositions queries the Polymarket Data API for a given address.
func (e *Engine) fetchPositions(ctx context.Context, addr string) []dataPosition {
	u := fmt.Sprintf("%s/positions?user=%s&sizeThreshold=0", dataAPIBaseURL, addr)
	return e.doFetchPositions(ctx, u, addr)
}

// doFetchPositions performs the HTTP GET for a positions URL and decodes the response.
func (e *Engine) doFetchPositions(ctx context.Context, u, addr string) []dataPosition {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		slog.Error("fetch positions: create request", "err", err)
		return nil
	}
	req.Header.Set("Accept", "application/json")

	resp, err := dataHTTPClient.Do(req)
	if err != nil {
		slog.Error("fetch positions: request failed", "addr", addr, "err", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		slog.Error("fetch positions: unexpected status", "addr", addr, "status", resp.StatusCode, "body", string(body))
		return nil
	}

	var positions []dataPosition
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&positions); err != nil {
		slog.Error("fetch positions: decode failed", "addr", addr, "err", err)
		return nil
	}
	return positions
}

// startReportServer launches a background HTTP server for live report pages.
func (e *Engine) startReportServer() {
	mux := http.NewServeMux()
	mux.Handle("/static/", web.StaticHandler())
	mux.Handle("/data", e.collector)

	// Multi-price paper trading report.
	papers := make([]*PaperTrader, len(e.paperSlots))
	for i, s := range e.paperSlots {
		papers[i] = s.paper
	}
	e.paperHandler = NewMultiPaperHandler(papers, e.store, e.cfg.DryRun)
	mux.Handle("/paper", e.paperHandler)
	mux.HandleFunc("/api/paper/data", e.paperHandler.ServeAPI)
	mux.HandleFunc("/api/paper/stream", e.paperHandler.ServeSSEJSON)

	// Optional backtest handler injected via SetExtraHandler.
	if e.extraHandler != nil {
		// Wire live stats to backtest handler for header display.
		type liveStatsSettable interface {
			SetLiveStatsFunc(func() LiveHeaderStats)
		}
		if bt, ok := e.extraHandler.(liveStatsSettable); ok {
			bt.SetLiveStatsFunc(e.liveHeaderStats)
		}
		mux.Handle("/backtest", e.extraHandler)

		// JSON API for backtest data.
		type apiServable interface {
			ServeAPI(http.ResponseWriter, *http.Request)
		}
		if api, ok := e.extraHandler.(apiServable); ok {
			mux.HandleFunc("/api/backtest/data", api.ServeAPI)
		}
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>pmbot</title>
			<meta name="viewport" content="width=device-width, initial-scale=1.0">
			<link href="https://fonts.googleapis.com/css2?family=Outfit:wght@500;600&display=swap" rel="stylesheet">
			<style>body{font-family:'Outfit',system-ui,sans-serif;background:#020617;color:#f1f5f9;display:flex;justify-content:center;align-items:center;height:100vh;margin:0}
			a{color:#94a3b8;font-size:18px;font-weight:500;margin:12px;text-decoration:none;padding:14px 28px;border:1px solid rgba(51,65,85,0.5);border-radius:8px;transition:all .15s}
			a:hover{background:#0f172a;color:#f1f5f9;border-color:rgba(51,65,85,0.8)}
			@media(max-width:768px){body{flex-direction:column}a{font-size:15px;margin:6px;padding:12px 22px}}</style></head>
			<body><a href="/paper">Paper Trading</a><a href="/backtest">Backtest</a><a href="/data">Data</a></body></html>`)
	})

	go func() {
		slog.Info("report server started", "addr", "http://localhost"+e.cfg.ReportAddr)
		if err := http.ListenAndServe(e.cfg.ReportAddr, mux); err != nil {
			slog.Error("report server error", "err", err)
		}
	}()
}

// liveHeaderStats returns a snapshot of the current live trading stats
// for page header display (called from the backtest handler).
func (e *Engine) liveHeaderStats() LiveHeaderStats {
	var liveStats, paperStats AggStats
	startTime := time.Now()
	for _, slot := range e.paperSlots {
		slot.paper.mu.RLock()
		if slot.paper.startTime.Before(startTime) {
			startTime = slot.paper.startTime
		}
		agg := &paperStats
		if slot.live {
			agg = &liveStats
		}
		agg.Trades += len(slot.paper.trades)
		agg.Wins += slot.paper.wins
		agg.Losses += slot.paper.losses
		agg.TotalPnL += slot.paper.totalPnL
		slot.paper.mu.RUnlock()
	}
	for _, s := range []*AggStats{&liveStats, &paperStats} {
		s.Resolved = s.Wins + s.Losses
		if s.Resolved > 0 {
			s.WinRate = float64(s.Wins) / float64(s.Resolved) * 100
			s.AvgPnL = s.TotalPnL / float64(s.Resolved)
			s.HasResolved = true
		}
	}
	return LiveHeaderStats{
		DryRun:     e.cfg.DryRun,
		HasLive:    liveStats.Trades > 0,
		HasPaper:   paperStats.Trades > 0,
		LiveStats:  liveStats,
		PaperStats: paperStats,
		Duration:   time.Since(startTime).Truncate(time.Second),
	}
}

// --- Helpers ---

// Price range parsing regexes for Polymarket BTC market questions.
var (
	betweenRe = regexp.MustCompile(`(?i)\$?([\d,]+)\s+(?:and|to|-)\s+\$?([\d,]+)`)
	aboveRe   = regexp.MustCompile(`(?i)(?:\$?([\d,]+)\s+or\s+more|(?:above|greater\s+than|more\s+than)\s+\$?([\d,]+)|\$?([\d,]+)\s*\+)`)
	belowRe   = regexp.MustCompile(`(?i)(?:less\s+than\s+\$?([\d,]+)|below\s+\$?([\d,]+)|under\s+\$?([\d,]+))`)
)

// parseRangeFromQuestion extracts price bounds from a market question string.
// Returns (lower, upper) where 0 means unbounded.
func parseRangeFromQuestion(q string) (lower, upper float64) {
	// Try "between X and Y" pattern first.
	if m := betweenRe.FindStringSubmatch(q); m != nil {
		lower = parseDollar(m[1])
		upper = parseDollar(m[2])
		if lower > upper {
			lower, upper = upper, lower
		}
		return lower, upper
	}

	// Try "above X" / "X or more" pattern.
	if m := aboveRe.FindStringSubmatch(q); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				return parseDollar(g), 0
			}
		}
	}

	// Try "below X" / "less than X" pattern.
	if m := belowRe.FindStringSubmatch(q); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				return 0, parseDollar(g)
			}
		}
	}

	// Fallback: no range detected.
	return 0, 0
}

// parseDollar converts "100,000" or "100000" to 100000.0.
func parseDollar(s string) float64 {
	s = strings.ReplaceAll(s, ",", "")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func formatRange(lower, upper float64) string {
	switch {
	case lower > 0 && upper > 0:
		return fmt.Sprintf("[%.0f, %.0f]", lower, upper)
	case lower > 0:
		return fmt.Sprintf("[%.0f, +inf)", lower)
	case upper > 0:
		return fmt.Sprintf("(-inf, %.0f]", upper)
	default:
		return "[unknown]"
	}
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// truncID truncates a hex ID for log output, showing the first 8 and last 4 characters.
func truncID(id string) string {
	if len(id) <= 16 {
		return id
	}
	return id[:8] + "..." + id[len(id)-4:]
}

// truncCondID truncates a condition ID for log output, showing the first 16 characters.
func truncCondID(id string) string {
	if len(id) <= 20 {
		return id
	}
	return id[:16] + "..."
}

func parseClobTokenIDs(raw string) (yesToken, noToken string, err error) {
	if raw == "" {
		return "", "", fmt.Errorf("empty clobTokenIds")
	}
	var tokens []string
	if err := json.Unmarshal([]byte(raw), &tokens); err != nil {
		return "", "", fmt.Errorf("unmarshal clobTokenIds: %w", err)
	}
	if len(tokens) < 2 {
		return "", "", fmt.Errorf("expected 2 tokens, got %d", len(tokens))
	}
	return tokens[0], tokens[1], nil
}

// --- Paper trading helpers ---

// paperSide determines whether a token ID corresponds to "Up" (YES) or "Down" (NO).
func (e *Engine) paperSide(tokenID string) string {
	for _, m := range e.markets {
		if tokenID == m.yesTokenID {
			return "Up"
		}
		if tokenID == m.noTokenID {
			return "Down"
		}
	}
	return "Unknown"
}

// currentWindowStart returns the start time of the current in-progress 5m candle.
func (e *Engine) currentWindowStart() time.Time {
	if c, ok := e.candles.CurrentCandle(5 * time.Minute); ok {
		return c.Start
	}
	return time.Time{}
}

// resolveCompletedWindow checks if a new 5m candle has completed and resolves
// paper trades and collector data for that window.
// Returns true if a window was resolved (used to trigger immediate market rotation).
func (e *Engine) resolveCompletedWindow() bool {
	c, ok := e.candles.LastCompleted(5 * time.Minute)
	if !ok {
		return false
	}
	if c.Start.Equal(e.lastResolved5m) {
		return false // already resolved
	}
	e.lastResolved5m = c.Start

	dir := string(c.Direction)
	e.lastLocalDir = dir // save for comparison with official market_resolved
	e.collector.ResolveWindow(c.Start, dir, c.Close)
	for _, slot := range e.paperSlots {
		slot.paper.ResolveWindow(c.Start, dir)
		slot.windowRetries = 0 // reset FAK retry counter for new window
	}
	// Persist paper trade resolution to DB.
	if e.store != nil {
		if err := e.store.ResolvePaperTrades(c.Start, dir); err != nil {
			slog.Warn("failed to persist paper trade resolution", "window", c.Start, "err", err)
		}
	}

	// Enqueue sells for live slot positions before clearing.
	// The background sell goroutine will place orders via REST API.
	e.enqueuePendingSells()

	// For dynamic markets, the old window's snapshots belong to a different
	// market slug and must be cleared unconditionally. Paper positions are
	// settled by market resolution and should not block snapshot clearing.
	if e.isDynamic {
		e.snapshots.Reset()
		for _, slot := range e.paperSlots {
			clear(slot.positions)
			clear(slot.entryPrices)
			clear(slot.entryElapsed)
		}
	}
	e.notifyDashboard()

	// Notify backtest handler to incrementally load the new window into its cache.
	if e.extraHandler != nil {
		type windowNotifier interface {
			NotifyNewWindow()
		}
		if wn, ok := e.extraHandler.(windowNotifier); ok {
			wn.NotifyNewWindow()
		}
	}

	return true
}

// handleMarketResolved processes the official market resolution from the
// Polymarket WebSocket. If the official direction differs from the local
// candle-based direction, paper trade results are corrected.
func (e *Engine) handleMarketResolved(evt ws.MarketEvent) {
	// Determine official direction from winning_outcome.
	var dir string
	switch evt.WinningOutcome {
	case "Yes", "Up":
		dir = "Up"
	case "No", "Down":
		dir = "Down"
	default:
		slog.Warn("market_resolved: unknown winning outcome", "outcome", evt.WinningOutcome)
		return
	}

	// Check if this resolution is for our tracked markets (current or previous).
	matched := false
	for _, aid := range evt.AssetsIDs {
		for _, m := range e.markets {
			if aid == m.yesTokenID || aid == m.noTokenID {
				matched = true
				break
			}
		}
		if matched {
			break
		}
		for _, tok := range e.prevMarketTokens {
			if aid == tok {
				matched = true
				break
			}
		}
		if matched {
			break
		}
	}
	if !matched {
		return
	}

	windowStart := e.lastResolved5m
	if windowStart.IsZero() {
		slog.Warn("market_resolved: no local resolution to compare",
			"dir", dir, "winning_asset", truncID(evt.WinningAssetID))
		return
	}

	if e.lastLocalDir == dir {
		slog.Info("market_resolved: confirmed",
			"window_end", windowStart.Add(5*time.Minute).Format("15:04:05"), "dir", dir)
		return
	}

	// Direction mismatch: correct paper trades with official result.
	slog.Warn("market_resolved: direction mismatch, correcting",
		"window_end", windowStart.Add(5*time.Minute).Format("15:04:05"),
		"local", e.lastLocalDir, "official", dir)

	for _, slot := range e.paperSlots {
		slot.paper.CorrectResolution(windowStart, dir)
	}
	e.collector.CorrectDirection(windowStart, dir)
	if e.store != nil {
		if err := e.store.CorrectPaperTrades(windowStart, dir); err != nil {
			slog.Warn("failed to persist corrected paper trades", "err", err)
		}
		if err := e.store.CorrectWindowDirection(windowStart, dir); err != nil {
			slog.Warn("failed to correct window direction", "err", err)
		}
	}
	e.lastLocalDir = dir
	e.notifyDashboard()
}

// --- Background sell goroutine ---

// enqueueEarlyExitSell enqueues a single early-exit sell into the pending sell
// queue so the background sell goroutine can attempt a relayer redeem.
func (e *Engine) enqueueEarlyExitSell(slot *paperSlot, sig strategy.Signal) {
	var conditionID string
	var outcomeIndex int
	for _, m := range e.markets {
		if m.yesTokenID == sig.TokenID {
			conditionID = m.conditionID
			outcomeIndex = 0
			break
		}
		if m.noTokenID == sig.TokenID {
			conditionID = m.conditionID
			outcomeIndex = 1
			break
		}
	}

	e.pendingSellsMu.Lock()
	e.pendingSells = append(e.pendingSells, pendingSell{
		slotLabel:    slot.paper.label,
		tokenID:      sig.TokenID,
		conditionID:  conditionID,
		outcomeIndex: outcomeIndex,
		shares:       sig.Size,
		tickSize:     sig.TickSize,
		negRisk:      sig.NegRisk,
		feeRateBps:   sig.FeeRateBps,
		addedAt:      time.Now(),
	})
	e.pendingSellsMu.Unlock()

	slog.Info("early exit fallback to relayer redeem",
		"slot", slot.paper.label,
		"token", truncID(sig.TokenID),
		"shares", sig.Size,
		"condition_id", truncCondID(conditionID))
}

// enqueuePendingSells moves live slot positions into the pending sell queue.
// Called from resolveCompletedWindow before positions are cleared.
func (e *Engine) enqueuePendingSells() {
	e.pendingSellsMu.Lock()
	defer e.pendingSellsMu.Unlock()

	now := time.Now()
	for _, slot := range e.paperSlots {
		if !slot.live {
			continue
		}
		for tokenID, shares := range slot.positions {
			if shares <= 0 {
				continue
			}
			var tickSize string
			var negRisk bool
			var feeRateBps int
			var conditionID string
			outcomeIndex := 0 // default YES
			for _, m := range e.markets {
				if m.yesTokenID == tokenID || m.noTokenID == tokenID {
					tickSize = m.tickSize
					negRisk = m.negRisk
					feeRateBps = m.feeRateBps
					conditionID = m.conditionID
					if m.noTokenID == tokenID {
						outcomeIndex = 1
					}
					break
				}
			}
			e.pendingSells = append(e.pendingSells, pendingSell{
				slotLabel:    slot.paper.label,
				tokenID:      tokenID,
				conditionID:  conditionID,
				outcomeIndex: outcomeIndex,
				shares:       shares,
				tickSize:     tickSize,
				negRisk:      negRisk,
				feeRateBps:   feeRateBps,
				addedAt:      now,
			})
			slog.Info("redeem enqueued",
				"slot", slot.paper.label,
				"token", truncID(tokenID),
				"shares", shares,
				"neg_risk", negRisk,
				"outcome_index", outcomeIndex,
				"condition_id", truncCondID(conditionID))
		}
	}
}

// sellLoop runs in a background goroutine and processes pending sells.
func (e *Engine) sellLoop(ctx context.Context) {
	ticker := time.NewTicker(sellPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// On shutdown, sell immediately regardless of window timing.
			e.pendingSellsMu.Lock()
			remaining := len(e.pendingSells)
			e.pendingSellsMu.Unlock()
			if remaining > 0 {
				slog.Info("processing remaining sells on shutdown", "count", remaining)
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				e.drainPendingSells(shutdownCtx)
				cancel()
			}
			return
		case <-ticker.C:
			e.processPendingSells(ctx)
		}
	}
}

// inSellWindow returns true if we're in the last 1 minute of the current 5m candle.
// Sells are deferred to this window so the previous window's market has time
// to build liquidity and reflect the outcome.
func (e *Engine) inSellWindow() bool {
	c, ok := e.candles.CurrentCandle(5 * time.Minute)
	if !ok {
		return false
	}
	elapsed := time.Since(c.Start)
	remaining := 5*time.Minute - elapsed
	return remaining <= sellWindowLeadTime && remaining > 0
}

// processPendingSells attempts to execute all queued sell orders.
// Only runs during the last 1 minute of the current 5m window.
func (e *Engine) processPendingSells(ctx context.Context) {
	e.pendingSellsMu.Lock()
	if len(e.pendingSells) == 0 {
		e.pendingSellsMu.Unlock()
		return
	}
	// Wait for the sell window (last 1 minute of current 5m candle).
	if !e.inSellWindow() {
		e.pendingSellsMu.Unlock()
		return
	}
	sells := e.pendingSells
	e.pendingSells = nil
	e.pendingSellsMu.Unlock()

	var retry []pendingSell
	for i := range sells {
		s := &sells[i]
		if e.executePendingSell(ctx, s) {
			continue
		}
		s.retries++
		if s.retries >= maxSellRetries || time.Since(s.addedAt) > maxSellAge {
			slog.Warn("pending sell abandoned",
				"slot", s.slotLabel,
				"token", truncID(s.tokenID),
				"retries", s.retries,
				"age", time.Since(s.addedAt).Truncate(time.Second))
			continue
		}
		retry = append(retry, *s)
	}

	if len(retry) > 0 {
		e.pendingSellsMu.Lock()
		e.pendingSells = append(e.pendingSells, retry...)
		e.pendingSellsMu.Unlock()
	}
}

// drainPendingSells processes all queued sells immediately, bypassing the
// sell window check. Used during shutdown to avoid leaving positions open.
func (e *Engine) drainPendingSells(ctx context.Context) {
	e.pendingSellsMu.Lock()
	if len(e.pendingSells) == 0 {
		e.pendingSellsMu.Unlock()
		return
	}
	sells := e.pendingSells
	e.pendingSells = nil
	e.pendingSellsMu.Unlock()

	for i := range sells {
		e.executePendingSell(ctx, &sells[i])
	}
}

// executePendingSell redeems outcome tokens via the Polymarket relayer.
// Instead of selling on the order book (which has no liquidity after window rotation),
// it calls redeemPositions through the proxy wallet to convert winning tokens to USDC.e.
// For neg-risk markets, routes through the NegRiskAdapter contract.
// Returns true if done (success or permanent failure); false to retry later.
func (e *Engine) executePendingSell(ctx context.Context, s *pendingSell) bool {
	log := slog.With("slot", s.slotLabel)
	if s.conditionID == "" {
		log.Error("pending redeem: missing conditionID",
			"token", truncID(s.tokenID))
		return true // permanent failure
	}

	log.Info("executing pending redeem",
		"token", truncID(s.tokenID),
		"shares", s.shares,
		"neg_risk", s.negRisk,
		"outcome_index", s.outcomeIndex,
		"condition_id", truncCondID(s.conditionID),
		"attempt", s.retries)

	// Build redeem request with neg-risk awareness.
	req := clob.RedeemRequest{
		ConditionID: s.conditionID,
		NegRisk:     s.negRisk,
	}
	if s.negRisk {
		onChainAmount := displayToOnChain(s.shares)
		if s.outcomeIndex == 0 {
			req.Amounts = [2]*big.Int{onChainAmount, big.NewInt(0)}
		} else {
			req.Amounts = [2]*big.Int{big.NewInt(0), onChainAmount}
		}
	}

	redeemCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	resp, err := e.clobClient.Redeem(redeemCtx, []clob.RedeemRequest{req})
	if err != nil {
		// Permanent config errors should not be retried.
		if errors.Is(err, clob.ErrNoSessionCredentials) || errors.Is(err, clob.ErrNoPrivateKey) {
			log.Error("pending redeem: permanent config error, will not retry",
				"token", truncID(s.tokenID),
				"err", err)
			return true
		}
		log.Error("pending redeem: relay submit failed",
			"token", truncID(s.tokenID),
			"err", err)
		return false // retry later
	}

	log.Info("pending redeem submitted",
		"tx_id", resp.TransactionID,
		"tx_hash", resp.TransactionHash,
		"state", resp.State,
		"token", truncID(s.tokenID),
		"shares", s.shares,
		"neg_risk", s.negRisk)

	// Do NOT record in redeems table here. The relay submit may succeed (HTTP 200)
	// but the on-chain tx can still fail. Let claimLoop handle redeems recording
	// after verifying on-chain state, which is more reliable.

	return true
}

// --- Fast restart recovery ---

// restartGracePeriod is the maximum gap between the last DB window end and now
// for the engine to restore candle history from DB instead of starting fresh.
const restartGracePeriod = 30 * time.Second

// tryFastRestart checks whether the engine can restore candle history from the
// database to skip unnecessary warmup after a quick restart. If the gap between
// the last stored window and now is within restartGracePeriod, it seeds the
// CandleAggregator with historical candles and pre-sets the current window's
// open price to avoid partial marking.
func (e *Engine) tryFastRestart() {
	if e.store == nil {
		return
	}

	latest, err := e.store.LatestWindow()
	if err != nil {
		slog.Warn("fast restart: failed to query latest window", "err", err)
		return
	}
	if latest == nil {
		slog.Info("cold start: no historical windows in db")
		return
	}

	windowEnd := latest.StartTime.Add(5 * time.Minute)
	gap := time.Since(windowEnd)

	if gap > restartGracePeriod {
		slog.Info("cold start: gap exceeds grace period, starting fresh",
			"gap", fmt.Sprintf("%.0fs", gap.Seconds()),
			"grace", restartGracePeriod)
		return
	}

	// Load recent completed windows from DB (enough to fill maxHist=20).
	rows, err := e.store.RecentWindows(20)
	if err != nil {
		slog.Warn("fast restart: failed to query recent windows", "err", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	// Convert DB rows to candles (reverse to oldest-first order).
	candles := make([]feed.Candle, len(rows))
	for i, r := range rows {
		dir := feed.DirectionDown
		if r.BTCClose >= r.BTCOpen {
			dir = feed.DirectionUp
		}
		candles[len(rows)-1-i] = feed.Candle{
			Open:      r.BTCOpen,
			Close:     r.BTCClose,
			High:      max(r.BTCOpen, r.BTCClose),
			Low:       min(r.BTCOpen, r.BTCClose),
			Start:     r.StartTime,
			End:       r.StartTime.Add(5 * time.Minute),
			Direction: dir,
			Samples:   r.SampleCount,
			Partial:   false,
		}
	}

	// Seed 5m candle history.
	e.candles.SeedHistory(5*time.Minute, candles)

	// Set lastResolved5m to prevent re-resolving seeded windows.
	e.lastResolved5m = rows[0].StartTime // rows[0] is the newest

	// Pre-set current window open from the last window's close price.
	// This prevents the first tick from being marked as partial.
	now := time.Now()
	currentWindowStart := now.Truncate(5 * time.Minute)
	e.candles.SeedCurrentOpen(5*time.Minute, latest.BTCClose, currentWindowStart)

	slog.Info("fast restart: restored candle history from db",
		"candles", len(candles),
		"gap", fmt.Sprintf("%.0fs", gap.Seconds()),
		"last_window", latest.StartTime.Format("15:04:05"),
		"seed_open", fmt.Sprintf("%.2f", latest.BTCClose))
}

// restoreSlotState recovers slot-level state after a restart to prevent
// duplicate orders in the same 5m window. It does two things:
//  1. Queries the DB for unresolved paper trades in the current window
//     and sets lastBuyWindow + positions on matching slots.
//  2. Syncs API positions (e.positions) to slot.positions so the strategy's
//     holding guard (YesShares/NoShares > 0) can block new buys.
//
// Must be called after rotateMarket() so that e.markets and e.positions
// are populated.
func (e *Engine) restoreSlotState() {
	windowStart := time.Now().Truncate(5 * time.Minute)

	// Phase 1: Restore lastBuyWindow from DB trades.
	if e.store != nil {
		trades, err := e.store.UnresolvedTradesForWindow(windowStart)
		if err != nil {
			slog.Warn("restore slot state: query unresolved trades failed", "err", err)
		} else if len(trades) > 0 {
			// Build slot label → slot map for fast lookup.
			slotMap := make(map[string]*paperSlot, len(e.paperSlots))
			for _, slot := range e.paperSlots {
				slotMap[slot.paper.Label()] = slot
			}

			for _, t := range trades {
				slot, ok := slotMap[t.SlotLabel]
				if !ok {
					continue
				}
				slot.lastBuyWindow = windowStart

				// Restore position from trade data so the strategy's
				// holding guard (YesShares/NoShares > 0) also works.
				for _, m := range e.markets {
					if t.Side == "Up" {
						slot.positions[m.yesTokenID] = t.Size
						slot.entryPrices[m.yesTokenID] = t.BuyPrice
					} else if t.Side == "Down" {
						slot.positions[m.noTokenID] = t.Size
						slot.entryPrices[m.noTokenID] = t.BuyPrice
					}
				}

				slog.Info("restore slot state: marked slot as filled",
					"slot", t.SlotLabel,
					"side", t.Side,
					"window", windowStart.Format("15:04:05"))
			}
		}
	}

	// Phase 2: Sync API positions to slots as a fallback.
	// This covers the case where the trade was placed but not recorded
	// in the DB (e.g., crash between order execution and DB write).
	e.posMu.RLock()
	defer e.posMu.RUnlock()
	if len(e.positions) == 0 {
		return
	}

	for _, slot := range e.paperSlots {
		if !slot.live {
			continue
		}
		for _, m := range e.markets {
			if shares, ok := e.positions[m.yesTokenID]; ok && shares > 0 {
				if slot.positions[m.yesTokenID] == 0 {
					slot.positions[m.yesTokenID] = shares
					slot.lastBuyWindow = windowStart
					slog.Info("restore slot state: synced API position",
						"slot", slot.paper.Label(),
						"side", "Up",
						"shares", shares)
				}
			}
			if shares, ok := e.positions[m.noTokenID]; ok && shares > 0 {
				if slot.positions[m.noTokenID] == 0 {
					slot.positions[m.noTokenID] = shares
					slot.lastBuyWindow = windowStart
					slog.Info("restore slot state: synced API position",
						"slot", slot.paper.Label(),
						"side", "Down",
						"shares", shares)
				}
			}
		}
	}
}
