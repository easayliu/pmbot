package strategy

import (
	"fmt"
	"math"
	"time"
)

// SpreadStrategy trades 5-minute BTC Up/Down markets based on price spread.
//
// Logic: in the final seconds of the window, if the price gap between Up and
// Down exceeds MinSpread, buy the dominant (higher-priced) side. The large
// spread implies high market confidence in the outcome direction.
//
// Resolution rule (same as BTCUpDownStrategy):
//
//	window close price >= open price -> Up (YES wins)
//	window close price <  open price -> Down (NO wins)
type SpreadStrategy struct {
	MaxCost       float64 // maximum cost per trade (USDC)
	EntryPrice    float64 // max price willing to pay (limit order ceiling)
	LateWindowSec float64 // only trade in last N seconds of the 5m window
	MinSpread     float64 // minimum |Up_ask - Down_ask| to trigger (e.g., 0.40)
}

// Name returns the strategy identifier.
func (s *SpreadStrategy) Name() string {
	return "spread"
}

// Validate checks that required parameters are configured.
func (s *SpreadStrategy) Validate() error {
	if s.MaxCost <= 0 {
		return fmt.Errorf("spread: max_cost must be positive, got %.2f", s.MaxCost)
	}
	if s.EntryPrice <= 0 || s.EntryPrice > 1.0 {
		return fmt.Errorf("spread: entry_price must be in (0, 1.0], got %.2f", s.EntryPrice)
	}
	if s.LateWindowSec <= 0 {
		return fmt.Errorf("spread: late_window_sec must be positive, got %.1f", s.LateWindowSec)
	}
	if s.MinSpread <= 0 || s.MinSpread >= 1.0 {
		return fmt.Errorf("spread: min_spread must be in (0, 1.0), got %.2f", s.MinSpread)
	}
	return nil
}

// Evaluate decides whether to buy based on Up/Down price spread in the late window.
func (s *SpreadStrategy) Evaluate(state MarketState) Signal {
	if state.BTCSpotPrice == 0 {
		return Signal{Action: Hold, Reason: "no_data: BTC price unavailable"}
	}
	if len(state.Markets) == 0 {
		return Signal{Action: Hold, Reason: "no_data: market data unavailable"}
	}

	mkt, found := findUpDownMarket(state.Markets)
	if !found {
		return Signal{Action: Hold, Reason: "no_market: Up/Down market not found"}
	}

	// Already holding a position — just hold until window ends.
	if mkt.YesShares > 0 {
		return Signal{Action: Hold, Reason: fmt.Sprintf("holding: side=Up shares=%.2f", mkt.YesShares)}
	}
	if mkt.NoShares > 0 {
		return Signal{Action: Hold, Reason: fmt.Sprintf("holding: side=Down shares=%.2f", mkt.NoShares)}
	}

	candles := state.Candles

	// Partial candle guard.
	if candles.Partial5m {
		return Signal{Action: Hold, Reason: "partial_candle: open price unreliable, skipping window"}
	}

	// Only trade in the late window.
	if s.LateWindowSec <= 0 {
		return Signal{Action: Hold, Reason: "disabled: late_window_sec not configured"}
	}
	if candles.Remaining5m <= 0 {
		return Signal{Action: Hold, Reason: "window_closed"}
	}
	// Don't trade in the very last seconds (order may not fill).
	if candles.Remaining5m < 5*time.Second {
		return Signal{Action: Hold, Reason: "too_late: <5s remaining"}
	}
	lateWindowDur := time.Duration(s.LateWindowSec * float64(time.Second))
	if candles.Remaining5m > lateWindowDur {
		remainSec := int(candles.Remaining5m.Seconds())
		return Signal{Action: Hold, Reason: fmt.Sprintf("not_late: remain=%ds need<%ds",
			remainSec, int(s.LateWindowSec))}
	}

	// Need valid ask prices on both sides.
	if mkt.BestAsk <= 0 || mkt.NoBestAsk <= 0 {
		return Signal{Action: Hold, Reason: fmt.Sprintf("no_quotes: up_ask=%.2f down_ask=%.2f",
			mkt.BestAsk, mkt.NoBestAsk)}
	}

	// Compute spread and determine dominant side.
	spread := mkt.BestAsk - mkt.NoBestAsk
	absSpread := math.Abs(spread)
	remainSec := int(candles.Remaining5m.Seconds())

	if absSpread < s.MinSpread {
		return Signal{Action: Hold, Reason: fmt.Sprintf("spread_low: up=%.2f down=%.2f spread=%.2f need=%.2f remain=%ds",
			mkt.BestAsk, mkt.NoBestAsk, absSpread, s.MinSpread, remainSec)}
	}

	// Determine which side to buy.
	var fillPrice float64
	var tokenID, side string
	if mkt.BestAsk > mkt.NoBestAsk {
		fillPrice = mkt.BestAsk
		tokenID = mkt.YesTokenID
		side = "Up"
	} else {
		fillPrice = mkt.NoBestAsk
		tokenID = mkt.NoTokenID
		side = "Down"
	}

	// Entry limit check.
	if fillPrice > s.EntryPrice {
		return Signal{Action: Hold, Reason: fmt.Sprintf("no_fill: side=%s ask=%.2f limit=%.2f spread=%.2f remain=%ds",
			side, fillPrice, s.EntryPrice, absSpread, remainSec)}
	}

	size := s.MaxCost / fillPrice
	return Signal{
		Action:     Buy,
		TokenID:    tokenID,
		Side:       "BUY",
		Price:      fillPrice,
		Size:       size,
		MaxCost:    s.MaxCost,
		TickSize:   mkt.TickSize,
		NegRisk:    mkt.NegRisk,
		FeeRateBps: mkt.FeeRateBps,
		EntryLimit: s.EntryPrice,
		Reason: fmt.Sprintf("SPREAD %s: up_ask=%.2f down_ask=%.2f spread=%.2f (>=%.2f) remain=%ds",
			side, mkt.BestAsk, mkt.NoBestAsk, absSpread, s.MinSpread, remainSec),
	}
}
