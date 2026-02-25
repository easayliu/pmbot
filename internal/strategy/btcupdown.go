package strategy

import (
	"fmt"
	"math"
	"regexp"
	"time"
)

// BTCUpDownStrategy trades 5-minute BTC Up/Down markets on Polymarket.
//
// Supports three trading modes, evaluated in priority order:
//
//  1. Late-window sniper: In the final seconds of the window, direction is
//     highly certain. Uses reduced threshold, no rolling trend or decay checks.
//  2. Mean reversion: When a sharp early move shows signs of reversal (rolling
//     1m trend opposes candle direction), trade counter-trend at a discount.
//  3. Trend following: Standard dual-confirmation with streak reversal bias
//     and minimum signal strength filter.
//
// Resolution rule (Chainlink oracle):
//
//	window close price >= open price -> Up (YES wins)
//	window close price <  open price -> Down (NO wins)
type BTCUpDownStrategy struct {
	MaxCost        float64       // maximum cost per trade (USDC)
	EntryPrice     float64       // max price willing to pay (limit order ceiling); fills at market ask
	TrendThreshold float64       // minimum 5m candle change ($) to confirm trend direction
	MinElapsed     time.Duration // minimum time elapsed in current 5m window before trading

	// Volatility-adjusted threshold: replaces fixed TrendThreshold.
	// threshold = max(MinThreshold, VolSigma * volatility * sqrt(elapsedSec))
	// Set VolSigma=0 to fall back to fixed TrendThreshold (backward compat).
	VolSigma     float64 // n-sigma multiplier (e.g., 2.0)
	MinThreshold float64 // absolute floor for threshold ($)

	// Momentum decay: reject if deceleration exceeds n-sigma of per-minute volatility.
	// accel = speed1m - speed5m; reject if |decel| > AccelDecayVol * vol * sqrt(60).
	// Set 0 to disable.
	AccelDecayVol float64 // e.g., 1.5

	// Adaptive MinElapsed: stronger signals allow earlier entry.
	// adaptiveMin = max(MinElapsedFloor, MinElapsed / signalStrength)
	// Only active when VolSigma > 0.
	MinElapsedFloor time.Duration // absolute floor for adaptive elapsed

	// Per-slot elapsed scaling: higher entry price -> longer wait before entry.
	// scaledBase  = MinElapsed     * (EntryPrice / ElapsedPriceRef)
	// scaledFloor = MinElapsedFloor * (EntryPrice / ElapsedPriceRef)
	// Set 0 to disable scaling.
	ElapsedPriceRef float64

	// Rolling trend confirmation window: "1m" or "5m" (default "1m").
	// "1m" is more responsive to reversals, suitable for 5-min binary markets.
	// "5m" is more conservative, requires longer-term trend agreement.
	TrendConfirm string

	// Threshold discount when rolling trend confirms direction.
	// When trend_1m/5m agrees, effectiveThreshold = threshold * TrendDiscount.
	// Range: 0.0-1.0. Set 1.0 to disable.
	TrendDiscount float64

	// --- Late-window sniper mode ---
	// Enter in the final seconds when direction is highly certain.
	// Uses reduced threshold, skips rolling trend and momentum decay checks.
	// Set LateWindowSec=0 to disable.
	LateWindowSec          float64 // activate in last N seconds (e.g., 60)
	LateWindowThresholdMul float64 // threshold multiplier (e.g., 0.3 = 30% of base)

	// --- Mean reversion mode ---
	// Trade counter-trend when a sharp early move shows reversal signs.
	// Triggers when |Change5m| > MeanRevSigma * vol * sqrt(elapsed)
	// AND rolling 1m trend has reversed direction.
	// Set MeanRevSigma=0 to disable.
	MeanRevSigma      float64       // sigma for sharp move detection (e.g., 2.5)
	MeanRevMaxElapsed time.Duration // only active in first N seconds (e.g., 120s)

	// --- Streak reversal bias ---
	// After N consecutive same-direction windows, reduce threshold for
	// counter-streak direction to favor mean reversion.
	// Set StreakLen=0 to disable.
	StreakLen      int     // min consecutive windows (e.g., 3)
	StreakDiscount float64 // threshold multiplier for counter-streak (e.g., 0.6)

	// --- Minimum signal strength filter ---
	// Reject signals where |candle_change| / threshold < this value.
	// Filters marginal ~50/50 signals. Set 0 to disable.
	MinSignalStrength float64 // e.g., 1.2

	// --- Fair value gate ---
	// Theoretical fair price based on P(Up) = Φ(Δ / (σ × √t)).
	// Only enter when fair_value - market_ask >= FairValueEdge.
	// Set 0 to disable (backward compatible).
	FairValueEdge float64 // e.g., 0.05

	// --- Fair value stop-loss early exit ---
	// When holding a position, if fair_value < entryPrice * EarlyExitStopFactor,
	// emit a Sell signal to exit early and recover partial capital.
	// Set 0 to disable (backward compatible).
	EarlyExitStopFactor float64 // e.g., 0.7 means exit when FV drops below 70% of entry price
	EarlyExitMinHoldSec float64 // minimum seconds to hold before stop-loss can trigger (e.g., 30)
}

// Name returns the strategy identifier.
func (s *BTCUpDownStrategy) Name() string {
	return "btc_updown"
}

// normalCDF returns the standard normal cumulative distribution function Φ(x).
func normalCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

// fairValue computes the theoretical probability of the 5m window closing Up.
// Model: P(Up) = Φ(Δ / (σ × √t)), where Δ = current candle change from open,
// σ = EWMA volatility (per-second), t = remaining seconds.
func (s *BTCUpDownStrategy) fairValue(change5m, volatility, remainingSec float64) (pUp, pDown float64) {
	if volatility <= 0 || remainingSec <= 0 {
		// No vol or no time left — use directional bias only.
		// Resolution rule: close >= open → Up wins, so change5m == 0 means Up.
		if change5m >= 0 {
			return 0.99, 0.01
		}
		return 0.01, 0.99
	}
	z := change5m / (volatility * math.Sqrt(remainingSec))
	pUp = normalCDF(z)
	pDown = 1 - pUp
	return
}

// applyFairValueGate checks if the expected edge (fair_value - market_ask)
// meets the minimum FairValueEdge threshold. If not, converts Buy to Hold.
// When FairValueEdge <= 0, this is a no-op (backward compatible).
func (s *BTCUpDownStrategy) applyFairValueGate(sig Signal, mkt MarketInfo, change5m, volatility, remainingSec float64) Signal {
	if s.FairValueEdge <= 0 || sig.Action != Buy {
		return sig
	}

	pUp, pDown := s.fairValue(change5m, volatility, remainingSec)

	// Determine fair value and market ask for the side being bought.
	var fairVal, marketAsk float64
	if sig.TokenID == mkt.YesTokenID {
		fairVal = pUp
		marketAsk = sig.Price
	} else {
		fairVal = pDown
		marketAsk = sig.Price
	}

	edge := fairVal - marketAsk
	if edge < s.FairValueEdge {
		return Signal{
			Action:     Hold,
			Threshold:  sig.Threshold,
			EntryLimit: sig.EntryLimit,
			Reason: fmt.Sprintf("fair_value_gate: fv=%.3f ask=%.3f edge=%.3f need=%.3f | %s",
				fairVal, marketAsk, edge, s.FairValueEdge, sig.Reason),
		}
	}

	return sig
}

// checkEarlyExit evaluates whether a held position should be exited early
// based on fair value dropping below a stop-loss threshold.
// isYes indicates whether the position is YES (Up) or NO (Down).
// Returns a Sell signal if stop-loss triggers, otherwise Hold.
func (s *BTCUpDownStrategy) checkEarlyExit(mkt MarketInfo, candles CandleState, trend PriceTrend, isYes bool) Signal {
	if s.EarlyExitStopFactor <= 0 {
		return Signal{Action: Hold}
	}
	if mkt.EntryPrice <= 0 {
		return Signal{Action: Hold}
	}

	// Minimum hold time: prevent jitter-based exits right after entry.
	holdDuration := candles.Elapsed5m - mkt.EntryElapsed
	minHold := time.Duration(s.EarlyExitMinHoldSec * float64(time.Second))
	if holdDuration < minHold {
		return Signal{Action: Hold}
	}

	// Need trend data for FV calculation.
	if !trend.IsReady() || trend.Volatility <= 0 {
		return Signal{Action: Hold}
	}

	remainingSec := candles.Remaining5m.Seconds()
	pUp, pDown := s.fairValue(candles.Change5m, trend.Volatility, remainingSec)

	var fairVal float64
	var tokenID string
	var bestBid float64
	var shares float64
	var side string
	if isYes {
		fairVal = pUp
		tokenID = mkt.YesTokenID
		bestBid = mkt.BestBid
		shares = mkt.YesShares
		side = "Up"
	} else {
		fairVal = pDown
		tokenID = mkt.NoTokenID
		bestBid = mkt.NoBestBid
		shares = mkt.NoShares
		side = "Down"
	}

	stopLevel := mkt.EntryPrice * s.EarlyExitStopFactor
	if fairVal >= stopLevel {
		return Signal{Action: Hold}
	}

	// No bid available — cannot exit.
	if bestBid <= 0 {
		return Signal{Action: Hold}
	}

	return Signal{
		Action:  Sell,
		TokenID: tokenID,
		Side:    "SELL",
		Price:   bestBid,
		Size:    shares,
		TickSize: mkt.TickSize,
		NegRisk:  mkt.NegRisk,
		FeeRateBps: mkt.FeeRateBps,
		Reason: fmt.Sprintf("EARLY EXIT %s: fv=%.3f < stop=%.3f (entry=%.3f*%.2f) bid=%.3f hold=%ds",
			side, fairVal, stopLevel, mkt.EntryPrice, s.EarlyExitStopFactor, bestBid, int(holdDuration.Seconds())),
	}
}

// Evaluate decides whether to buy or hold.
//
// Evaluates three modes in priority order:
//  1. Late-window sniper (if enabled and in final seconds)
//  2. Mean reversion (if enabled and sharp early move detected)
//  3. Trend following (standard, with streak bias and signal strength filter)
//
// Trend-following and late-window Buy signals are passed through the fair
// value gate. Mean reversion signals skip the gate because the FV model
// assumes Brownian motion (current direction persists), which contradicts
// mean reversion's premise (direction reverses). MR has its own entry
// filters: N-sigma sharp move + rolling 1m trend reversal confirmation.
func (s *BTCUpDownStrategy) Evaluate(state MarketState) Signal {
	// --- Step 1: Guards (BTC price, markets, position) ---
	if state.BTCSpotPrice == 0 {
		return Signal{Action: Hold, Reason: "no_data: BTC price unavailable"}
	}
	if len(state.Markets) == 0 {
		return Signal{Action: Hold, Reason: "no_data: market data unavailable"}
	}

	mkt, found := s.findUpDownMarket(state.Markets)
	if !found {
		return Signal{Action: Hold, Reason: "no_market: Up/Down market not found"}
	}

	// If already in a position, check for early exit stop-loss.
	// Normal hold/sell at window end is handled by the engine.
	if mkt.YesShares > 0 {
		if sig := s.checkEarlyExit(mkt, state.Candles, state.Trend, true); sig.Action == Sell {
			return sig
		}
		return Signal{Action: Hold, Reason: fmt.Sprintf("holding: side=Up shares=%.2f", mkt.YesShares)}
	}
	if mkt.NoShares > 0 {
		if sig := s.checkEarlyExit(mkt, state.Candles, state.Trend, false); sig.Action == Sell {
			return sig
		}
		return Signal{Action: Hold, Reason: fmt.Sprintf("holding: side=Down shares=%.2f", mkt.NoShares)}
	}

	candles := state.Candles
	trend := state.Trend

	// --- Step 2: Partial candle guard ---
	// Skip trading when the current 5m candle has an unreliable open price
	// (e.g., bot started mid-window). The candle change cannot be trusted.
	if candles.Partial5m {
		return Signal{
			Action:     Hold,
			Reason:     "partial_candle: open price unreliable, skipping window",
			EntryLimit: s.EntryPrice,
		}
	}

	// --- Step 3: Trend readiness ---
	if !trend.IsReady() {
		return Signal{
			Action: Hold,
			Reason: fmt.Sprintf("trend_warmup: samples=%d/60", trend.Samples),
		}
	}

	// --- Step 4: Compute vol-adjusted threshold ---
	baseThreshold := s.computeThreshold(trend.Volatility, candles.Elapsed5m)

	// --- Step 5-7: Mode evaluation (produce signal) ---
	var sig Signal
	skipFairValue := false
	if s.isLateWindow(candles) {
		sig = s.evaluateLateWindow(mkt, candles, trend, baseThreshold)
	} else if s.MeanRevSigma > 0 && trend.Volatility > 0 {
		if meanSig, ok := s.evaluateMeanReversion(mkt, candles, trend); ok {
			sig = meanSig
			// Mean reversion is counter-trend: the FV model (Brownian motion)
			// assumes current direction persists, so it would always reject
			// counter-trend entries. MR relies on its own filters instead.
			skipFairValue = true
		} else {
			sig = s.evaluateTrendFollowing(mkt, candles, trend, baseThreshold)
		}
	} else {
		sig = s.evaluateTrendFollowing(mkt, candles, trend, baseThreshold)
	}

	// --- Step 8: Fair value gate (trend-following and late-window only) ---
	if !skipFairValue {
		sig = s.applyFairValueGate(sig, mkt, candles.Change5m, trend.Volatility, candles.Remaining5m.Seconds())
	}
	return sig
}

// isLateWindow returns true if the late-window sniper mode should activate.
func (s *BTCUpDownStrategy) isLateWindow(candles CandleState) bool {
	if s.LateWindowSec <= 0 {
		return false
	}
	if candles.Remaining5m <= 0 {
		return false
	}
	// Don't trade in the very last seconds (order may not fill).
	if candles.Remaining5m < 5*time.Second {
		return false
	}
	lateWindowDur := time.Duration(s.LateWindowSec * float64(time.Second))
	return candles.Remaining5m <= lateWindowDur
}

// evaluateLateWindow handles the late-window sniper mode.
// In the final seconds of the window, direction is highly certain.
// Uses reduced threshold, no rolling trend or momentum decay checks.
func (s *BTCUpDownStrategy) evaluateLateWindow(mkt MarketInfo, candles CandleState, _ PriceTrend, baseThreshold float64) Signal {
	mul := s.LateWindowThresholdMul
	if mul <= 0 {
		mul = 0.3 // default: 30% of base threshold
	}
	threshold := baseThreshold * mul
	// Apply MinThreshold floor to prevent noise-level thresholds in low-vol environments.
	// Without this, vol=1.0 yields threshold=$2.33 which is pure noise for BTC.
	if s.MinThreshold > 0 && threshold < s.MinThreshold {
		threshold = s.MinThreshold
	}

	remainSec := int(candles.Remaining5m.Seconds())
	hold := func(reason string) Signal {
		return Signal{Action: Hold, Reason: reason, Threshold: threshold, EntryLimit: s.EntryPrice}
	}

	candleChange := candles.Change5m

	// Signal strength for late-window entry.
	strength := 0.0
	if threshold > 0 {
		strength = math.Abs(candleChange) / threshold
	}

	// Min signal strength filter.
	if s.MinSignalStrength > 0 && strength < s.MinSignalStrength {
		return hold(fmt.Sprintf("late_weak: strength=%.1f need=%.1f remain=%ds candle=%+.0f",
			strength, s.MinSignalStrength, remainSec, candleChange))
	}

	// Up direction.
	if candleChange > threshold {
		if mkt.BestAsk <= 0 {
			return hold(fmt.Sprintf("late_no_fill: side=Up ask=0 candle=%+.0f remain=%ds",
				candleChange, remainSec))
		}
		if mkt.BestAsk <= s.EntryPrice {
			fillPrice := mkt.BestAsk
			size := s.MaxCost / fillPrice
			return Signal{
				Action:     Buy,
				TokenID:    mkt.YesTokenID,
				Side:       "BUY",
				Price:      fillPrice,
				Size:       size,
				MaxCost:    s.MaxCost,
				TickSize:   mkt.TickSize,
				NegRisk:    mkt.NegRisk,
				FeeRateBps: mkt.FeeRateBps,
				Threshold:  threshold,
				EntryLimit: s.EntryPrice,
				Reason: fmt.Sprintf("LATE SNIPER UP: candle=%+.0f (>%.0f) ask=%.2f remain=%ds strength=%.1f",
					candleChange, threshold, fillPrice, remainSec, strength),
			}
		}
		return hold(fmt.Sprintf("late_no_fill: side=Up ask=%.2f limit=%.2f candle=%+.0f remain=%ds",
			mkt.BestAsk, s.EntryPrice, candleChange, remainSec))
	}

	// Down direction.
	if candleChange < -threshold {
		if mkt.NoBestAsk <= 0 {
			return hold(fmt.Sprintf("late_no_fill: side=Down ask=0 candle=%+.0f remain=%ds",
				candleChange, remainSec))
		}
		if mkt.NoBestAsk <= s.EntryPrice {
			fillPrice := mkt.NoBestAsk
			size := s.MaxCost / fillPrice
			return Signal{
				Action:     Buy,
				TokenID:    mkt.NoTokenID,
				Side:       "BUY",
				Price:      fillPrice,
				Size:       size,
				MaxCost:    s.MaxCost,
				TickSize:   mkt.TickSize,
				NegRisk:    mkt.NegRisk,
				FeeRateBps: mkt.FeeRateBps,
				Threshold:  threshold,
				EntryLimit: s.EntryPrice,
				Reason: fmt.Sprintf("LATE SNIPER DOWN: candle=%+.0f (<%.0f) ask=%.2f remain=%ds strength=%.1f",
					candleChange, -threshold, fillPrice, remainSec, strength),
			}
		}
		return hold(fmt.Sprintf("late_no_fill: side=Down ask=%.2f limit=%.2f candle=%+.0f remain=%ds",
			mkt.NoBestAsk, s.EntryPrice, candleChange, remainSec))
	}

	return hold(fmt.Sprintf("late_no_trend: candle=%+.0f threshold=%.0f remain=%ds",
		candleChange, threshold, remainSec))
}

// evaluateMeanReversion checks for counter-trend opportunities in early window.
// Triggers when candle shows a sharp move but rolling 1m trend has reversed.
// Returns (signal, true) if a trade is generated, (_, false) to fall through.
func (s *BTCUpDownStrategy) evaluateMeanReversion(mkt MarketInfo, candles CandleState, trend PriceTrend) (Signal, bool) {
	// Only active in early window.
	// Default to 120s if not explicitly configured.
	maxElapsed := s.MeanRevMaxElapsed
	if maxElapsed <= 0 {
		maxElapsed = 120 * time.Second
	}
	if candles.Elapsed5m > maxElapsed {
		return Signal{}, false
	}

	// Need minimum elapsed for reliable reversal detection.
	if candles.Elapsed5m < 30*time.Second {
		return Signal{}, false
	}

	// Sharp move threshold: N sigma of expected move for elapsed time.
	elapsedSec := candles.Elapsed5m.Seconds()
	moveThreshold := s.MeanRevSigma * trend.Volatility * math.Sqrt(elapsedSec)
	if moveThreshold <= 0 {
		return Signal{}, false
	}

	candleChange := candles.Change5m

	// Not a sharp enough move.
	if math.Abs(candleChange) < moveThreshold {
		return Signal{}, false
	}

	// Sharp drop + 1m trend recovering -> buy Up (counter-trend).
	if candleChange < -moveThreshold && trend.Change1m > 0 {
		if mkt.BestAsk <= 0 || mkt.BestAsk > s.EntryPrice {
			return Signal{}, false
		}
		fillPrice := mkt.BestAsk
		size := s.MaxCost / fillPrice
		return Signal{
			Action:     Buy,
			TokenID:    mkt.YesTokenID,
			Side:       "BUY",
			Price:      fillPrice,
			Size:       size,
			MaxCost:    s.MaxCost,
			TickSize:   mkt.TickSize,
			NegRisk:    mkt.NegRisk,
			FeeRateBps: mkt.FeeRateBps,
			Threshold:  moveThreshold,
			EntryLimit: s.EntryPrice,
			Reason: fmt.Sprintf("MEAN REV UP: candle=%+.0f trend_1m=%+.0f (reversal) ask=%.2f elapsed=%ds",
				candleChange, trend.Change1m, fillPrice, int(elapsedSec)),
		}, true
	}

	// Sharp rise + 1m trend fading -> buy Down (counter-trend).
	if candleChange > moveThreshold && trend.Change1m < 0 {
		if mkt.NoBestAsk <= 0 || mkt.NoBestAsk > s.EntryPrice {
			return Signal{}, false
		}
		fillPrice := mkt.NoBestAsk
		size := s.MaxCost / fillPrice
		return Signal{
			Action:     Buy,
			TokenID:    mkt.NoTokenID,
			Side:       "BUY",
			Price:      fillPrice,
			Size:       size,
			MaxCost:    s.MaxCost,
			TickSize:   mkt.TickSize,
			NegRisk:    mkt.NegRisk,
			FeeRateBps: mkt.FeeRateBps,
			Threshold:  moveThreshold,
			EntryLimit: s.EntryPrice,
			Reason: fmt.Sprintf("MEAN REV DOWN: candle=%+.0f trend_1m=%+.0f (reversal) ask=%.2f elapsed=%ds",
				candleChange, trend.Change1m, fillPrice, int(elapsedSec)),
		}, true
	}

	return Signal{}, false
}

// evaluateTrendFollowing implements the standard trend-following logic
// with streak reversal bias and minimum signal strength filter.
func (s *BTCUpDownStrategy) evaluateTrendFollowing(mkt MarketInfo, candles CandleState, trend PriceTrend, baseThreshold float64) Signal {
	candleChange := candles.Change5m
	rollingChange := trend.Change1m
	if s.TrendConfirm == "5m" {
		rollingChange = trend.Change5m
	}

	discount := s.TrendDiscount

	hold := func(reason string, threshold float64) Signal {
		return Signal{Action: Hold, Reason: reason, Threshold: threshold, EntryLimit: s.EntryPrice}
	}

	signalStrength := 0.0
	if baseThreshold > 0 {
		signalStrength = math.Abs(candleChange) / baseThreshold
	}
	adaptiveMin := s.adaptiveMinElapsed(signalStrength)

	// Elapsed guard (adaptive).
	if candles.Elapsed5m < adaptiveMin {
		return hold(fmt.Sprintf("too_young: elapsed=%s need=%s strength=%.1f",
			candles.Elapsed5m.Truncate(time.Second), adaptiveMin.Truncate(time.Second), signalStrength),
			baseThreshold)
	}

	// Min signal strength filter: reject marginal signals.
	if s.MinSignalStrength > 0 && signalStrength < s.MinSignalStrength {
		return hold(fmt.Sprintf("weak_signal: strength=%.1f need=%.1f candle=%+.0f threshold=%.0f",
			signalStrength, s.MinSignalStrength, candleChange, baseThreshold),
			baseThreshold)
	}

	// Streak reversal bias: adjust thresholds based on recent window directions.
	upStreakMul, downStreakMul := s.streakBias(candles.RecentDirs5m)

	// Uptrend: rolling confirms up -> apply discount; otherwise require full threshold.
	// When streak bias and trend discount stack, clamp to MinThreshold floor
	// to prevent excessively low thresholds from triggering on noise.
	upThreshold := baseThreshold * upStreakMul
	if rollingChange > 0 {
		upThreshold *= discount
	}
	if s.MinThreshold > 0 && upThreshold < s.MinThreshold {
		upThreshold = s.MinThreshold
	}
	if candleChange > upThreshold && rollingChange > 0 {
		upStrength := candleChange / upThreshold

		// Momentum decay check.
		if reason, decaying := s.checkMomentumDecay(trend, true); decaying {
			return hold(fmt.Sprintf("momentum_decay: dir=up %s candle=%+.0f threshold=%.0f",
				reason, candleChange, upThreshold), upThreshold)
		}

		if mkt.BestAsk <= 0 {
			return hold(fmt.Sprintf("no_fill: side=Up ask=0 candle=%+.0f rolling=%+.0f threshold=%.0f",
				candleChange, rollingChange, upThreshold), upThreshold)
		}
		if mkt.BestAsk <= s.EntryPrice {
			fillPrice := mkt.BestAsk
			size := s.MaxCost / fillPrice
			streakTag := s.formatStreakTag(upStreakMul)
			return Signal{
				Action:     Buy,
				TokenID:    mkt.YesTokenID,
				Side:       "BUY",
				Price:      fillPrice,
				Size:       size,
				MaxCost:    s.MaxCost,
				TickSize:   mkt.TickSize,
				NegRisk:    mkt.NegRisk,
				FeeRateBps: mkt.FeeRateBps,
				Threshold:  upThreshold,
				EntryLimit: s.EntryPrice,
				Reason: fmt.Sprintf("TREND UP: candle=%+.0f rolling=%+.0f (>%.0f) ask=%.2f limit=%.2f cost=$%.2f strength=%.1f%s",
					candleChange, rollingChange, upThreshold, fillPrice, s.EntryPrice, s.MaxCost, upStrength, streakTag),
			}
		}
		return hold(fmt.Sprintf("no_fill: side=Up ask=%.2f limit=%.2f candle=%+.0f rolling=%+.0f threshold=%.0f",
			mkt.BestAsk, s.EntryPrice, candleChange, rollingChange, upThreshold), upThreshold)
	}

	// Downtrend: rolling confirms down -> apply discount; otherwise require full threshold.
	downThreshold := baseThreshold * downStreakMul
	if rollingChange < 0 {
		downThreshold *= discount
	}
	if s.MinThreshold > 0 && downThreshold < s.MinThreshold {
		downThreshold = s.MinThreshold
	}
	if candleChange < -downThreshold && rollingChange < 0 {
		downStrength := math.Abs(candleChange) / downThreshold

		// Momentum decay check.
		if reason, decaying := s.checkMomentumDecay(trend, false); decaying {
			return hold(fmt.Sprintf("momentum_decay: dir=down %s candle=%+.0f threshold=%.0f",
				reason, candleChange, downThreshold), downThreshold)
		}

		if mkt.NoBestAsk <= 0 {
			return hold(fmt.Sprintf("no_fill: side=Down ask=0 candle=%+.0f rolling=%+.0f threshold=%.0f",
				candleChange, rollingChange, downThreshold), downThreshold)
		}
		if mkt.NoBestAsk <= s.EntryPrice {
			fillPrice := mkt.NoBestAsk
			size := s.MaxCost / fillPrice
			streakTag := s.formatStreakTag(downStreakMul)
			return Signal{
				Action:     Buy,
				TokenID:    mkt.NoTokenID,
				Side:       "BUY",
				Price:      fillPrice,
				Size:       size,
				MaxCost:    s.MaxCost,
				TickSize:   mkt.TickSize,
				NegRisk:    mkt.NegRisk,
				FeeRateBps: mkt.FeeRateBps,
				Threshold:  downThreshold,
				EntryLimit: s.EntryPrice,
				Reason: fmt.Sprintf("TREND DOWN: candle=%+.0f rolling=%+.0f (<%.0f) ask=%.2f limit=%.2f cost=$%.2f strength=%.1f%s",
					candleChange, rollingChange, -downThreshold, fillPrice, s.EntryPrice, s.MaxCost, downStrength, streakTag),
			}
		}
		return hold(fmt.Sprintf("no_fill: side=Down ask=%.2f limit=%.2f candle=%+.0f rolling=%+.0f threshold=%.0f",
			mkt.NoBestAsk, s.EntryPrice, candleChange, rollingChange, downThreshold), downThreshold)
	}

	return hold(fmt.Sprintf("no_trend: candle=%+.0f rolling=%+.0f threshold=%.0f up_ask=%.2f down_ask=%.2f limit=%.2f",
		candleChange, rollingChange, baseThreshold, mkt.BestAsk, mkt.NoBestAsk, s.EntryPrice),
		baseThreshold)
}

// streakBias computes threshold multipliers based on recent consecutive windows.
// Returns (upMul, downMul): multipliers applied to base threshold.
// After N consecutive Up windows: upMul > 1 (harder to go Up), downMul < 1 (easier).
func (s *BTCUpDownStrategy) streakBias(recentDirs []string) (upMul, downMul float64) {
	if s.StreakLen <= 0 || s.StreakDiscount <= 0 || s.StreakDiscount >= 1.0 || len(recentDirs) < s.StreakLen {
		return 1.0, 1.0
	}

	allUp := true
	allDown := true
	for i := 0; i < s.StreakLen; i++ {
		if recentDirs[i] != "Up" {
			allUp = false
		}
		if recentDirs[i] != "Down" {
			allDown = false
		}
	}

	if allUp {
		// After consecutive ups: harder to go up, easier to go down.
		return 1.0 / s.StreakDiscount, s.StreakDiscount
	}
	if allDown {
		// After consecutive downs: easier to go up, harder to go down.
		return s.StreakDiscount, 1.0 / s.StreakDiscount
	}

	return 1.0, 1.0
}

// formatStreakTag returns a log tag when streak bias is active.
func (s *BTCUpDownStrategy) formatStreakTag(mul float64) string {
	if mul == 1.0 {
		return ""
	}
	return fmt.Sprintf(" streak=%.2fx", mul)
}

// computeThreshold returns the volatility-adjusted threshold.
// If VolSigma <= 0, falls back to fixed TrendThreshold.
func (s *BTCUpDownStrategy) computeThreshold(volatility float64, elapsed time.Duration) float64 {
	if s.VolSigma <= 0 {
		return s.TrendThreshold
	}

	minThresh := s.MinThreshold

	elapsedSec := elapsed.Seconds()
	if volatility <= 0 || elapsedSec <= 0 {
		return math.Max(minThresh, s.TrendThreshold)
	}

	volThreshold := s.VolSigma * volatility * math.Sqrt(elapsedSec)
	return math.Max(minThresh, volThreshold)
}

// adaptiveMinElapsed returns the adaptive minimum elapsed duration.
// Stronger signals (signalStrength > 1) allow earlier entry.
// If VolSigma <= 0, returns the base MinElapsed unchanged.
//
// Per-slot elapsed scaling (when ElapsedPriceRef > 0):
// Both base and floor are scaled by EntryPrice / ElapsedPriceRef before
// signal-strength adjustment. Higher entry prices wait longer; lower prices
// may enter earlier. An absolute floor of 15s prevents extreme low-price
// slots from having negligible wait times.
func (s *BTCUpDownStrategy) adaptiveMinElapsed(signalStrength float64) time.Duration {
	base := s.MinElapsed
	floor := s.MinElapsedFloor

	// Per-slot elapsed scaling by entry price.
	if s.ElapsedPriceRef > 0 && s.EntryPrice > 0 {
		scale := s.EntryPrice / s.ElapsedPriceRef
		base = time.Duration(float64(base) * scale)
		floor = time.Duration(float64(floor) * scale)

		// Absolute floor: prevent extreme low-price slots from near-zero wait.
		const absoluteFloor = 15 * time.Second
		if floor < absoluteFloor {
			floor = absoluteFloor
		}
	}

	if s.VolSigma <= 0 {
		return base
	}

	if signalStrength <= 1 {
		return base
	}

	adaptive := time.Duration(float64(base) / signalStrength)
	if adaptive < floor {
		return floor
	}
	return adaptive
}

// checkMomentumDecay checks if trend momentum is decelerating.
// Uses acceleration (speed1m - speed5m) normalized by per-minute volatility.
// Rejects when deceleration exceeds AccelDecayVol standard deviations.
// If AccelDecayVol <= 0, always returns false (disabled).
func (s *BTCUpDownStrategy) checkMomentumDecay(trend PriceTrend, isUptrend bool) (string, bool) {
	if s.AccelDecayVol <= 0 {
		return "", false
	}

	speed1m := trend.Speed1m
	speed5m := trend.Speed5m

	// Direction guard: speed1m must agree with trend direction.
	if isUptrend && speed1m <= 0 {
		return fmt.Sprintf("reversed: speed1m=%.1f<=0", speed1m), true
	}
	if !isUptrend && speed1m >= 0 {
		return fmt.Sprintf("reversed: speed1m=%.1f>=0", speed1m), true
	}

	// Normalize volatility from $/sec to $/min scale.
	volPerMin := trend.Volatility * math.Sqrt(60)
	if volPerMin <= 0 {
		return "", false
	}

	// Acceleration: positive = accelerating up, negative = accelerating down.
	accel := speed1m - speed5m
	threshold := s.AccelDecayVol * volPerMin

	// Uptrend: reject if decelerating too fast (accel << 0).
	if isUptrend && accel < -threshold {
		return fmt.Sprintf("decel=%.1f<%+.1f s1m=%.1f s5m=%.1f", accel, -threshold, speed1m, speed5m), true
	}
	// Downtrend: reject if decelerating too fast (accel >> 0, meaning downward speed is slowing).
	if !isUptrend && accel > threshold {
		return fmt.Sprintf("decel=%.1f>%+.1f s1m=%.1f s5m=%.1f", accel, threshold, speed1m, speed5m), true
	}

	return "", false
}

// upDownRe matches Up/Down directional keywords with word boundaries to avoid
// false positives like "setup", "cup", or range questions containing "above $100,000".
var upDownRe = regexp.MustCompile(`(?i)\b(go\s+up|higher|increase|rise)\b`)

// findUpDownMarket finds the market representing the Up/Down binary outcome.
// For 5m up/down events (the common case), the engine resolves to a single market
// where YES = Up and NO = Down, so single-market is checked first.
func (s *BTCUpDownStrategy) findUpDownMarket(markets []MarketInfo) (MarketInfo, bool) {
	if len(markets) == 1 {
		return markets[0], true
	}
	for _, m := range markets {
		if upDownRe.MatchString(m.Question) {
			return m, true
		}
	}
	return MarketInfo{}, false
}
