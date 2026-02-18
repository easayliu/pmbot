package engine

import (
	"log/slog"
	"time"

	"github.com/easay/pmbot/internal/strategy"
)

// RiskVerdict is the result of a pre-trade risk check.
type RiskVerdict int

const (
	VerdictPass          RiskVerdict = iota
	VerdictHold                      // signal converted to hold (e.g., already bought in window)
	VerdictDedup                     // duplicate signal suppressed
	VerdictCircuitBreaker            // daily limit reached
)

func (v RiskVerdict) String() string {
	switch v {
	case VerdictPass:
		return "PASS"
	case VerdictHold:
		return "HOLD"
	case VerdictDedup:
		return "DEDUP"
	case VerdictCircuitBreaker:
		return "CIRCUIT_BREAKER"
	default:
		return "UNKNOWN"
	}
}

// RiskManager validates trading signals before execution.
// It handles signal deduplication, window-level dedup, and circuit breakers.
type RiskManager struct {
	maxDailyOrders int
	maxDailyAmount float64

	dailyOrders int
	dailyAmount float64
	dailyReset  time.Time
}

// NewRiskManager creates a RiskManager with the given daily limits.
func NewRiskManager(maxOrders int, maxAmount float64) *RiskManager {
	return &RiskManager{
		maxDailyOrders: maxOrders,
		maxDailyAmount: maxAmount,
	}
}

// PreCheck validates a signal against slot-level dedup rules.
// It checks window dedup (already bought in this 5m window) and signal dedup
// (same action+token as last signal). Returns the potentially modified signal
// and a verdict.
func (rm *RiskManager) PreCheck(sig strategy.Signal, slot *paperSlot, windowStart time.Time) (strategy.Signal, RiskVerdict) {
	// Window dedup: suppress Buy if already bought in this 5m window.
	if sig.Action == strategy.Buy {
		if !windowStart.IsZero() && windowStart.Equal(slot.lastBuyWindow) {
			return strategy.Signal{Action: strategy.Hold, Reason: "already bought in this window"}, VerdictHold
		}
	}

	if sig.Action == strategy.Hold {
		return sig, VerdictHold
	}

	// Signal dedup: same action + same token as last evaluation.
	if sig.TokenID == slot.lastOrderedToken && sig.Action == slot.lastSignal {
		return sig, VerdictDedup
	}

	return sig, VerdictPass
}

// PreTrade validates a signal against portfolio-level risk limits (circuit breaker).
// Call this only for signals that passed PreCheck and will be sent to the exchange.
// Sell signals bypass the circuit breaker — stop-loss exits must not be blocked.
func (rm *RiskManager) PreTrade(sig strategy.Signal) RiskVerdict {
	if sig.Action == strategy.Sell {
		return VerdictPass
	}

	now := time.Now()

	// Reset counters at midnight.
	if now.After(rm.dailyReset) {
		rm.dailyOrders = 0
		rm.dailyAmount = 0
		y, m, d := now.Date()
		rm.dailyReset = time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
	}

	if rm.dailyOrders >= rm.maxDailyOrders {
		slog.Warn("circuit breaker triggered",
			"reason", "daily order limit", "orders", rm.dailyOrders, "max", rm.maxDailyOrders)
		return VerdictCircuitBreaker
	}

	orderCost := sig.MaxCost
	if orderCost <= 0 {
		orderCost = sig.Price * sig.Size
	}
	if rm.dailyAmount+orderCost > rm.maxDailyAmount {
		slog.Warn("circuit breaker triggered",
			"reason", "daily amount limit", "current", rm.dailyAmount, "order_cost", orderCost, "max", rm.maxDailyAmount)
		return VerdictCircuitBreaker
	}

	return VerdictPass
}

// RecordTrade updates risk counters after a successful trade execution.
func (rm *RiskManager) RecordTrade(cost float64) {
	rm.dailyOrders++
	rm.dailyAmount += cost
}
