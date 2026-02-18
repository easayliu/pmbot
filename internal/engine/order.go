package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/easay/pmbot/internal/clob"
	"github.com/easay/pmbot/internal/strategy"
	"github.com/easay/pmbot/internal/ws"
)

const (
	// maxOrderRetries is the max number of retries for transient API errors.
	maxOrderRetries = 3
)

// OrderResult is the outcome of an order execution attempt.
type OrderResult struct {
	Success  bool
	OrderID  string
	Status   string
	ErrorMsg string
}

// OrderManager handles order construction, execution, and retry logic.
// It is responsible for slippage protection and exchange interaction.
type OrderManager struct {
	client             *clob.Client
	maxSlippagePct     float64 // max price deviation from signal to current snapshot
	fakPriceBufferTicks int    // ticks added above best ask for FAK BUY orders
}

// NewOrderManager creates an OrderManager backed by the given CLOB client.
func NewOrderManager(client *clob.Client, maxSlippagePct float64, fakPriceBufferTicks int) *OrderManager {
	return &OrderManager{
		client:              client,
		maxSlippagePct:      maxSlippagePct,
		fakPriceBufferTicks: fakPriceBufferTicks,
	}
}

// Execute places a FAK market order with slippage protection and retry.
// slotLabel is used for logging only.
// marketPrice is the current best ask (for BUY) or best bid (for SELL) from the WS snapshot.
// Pass 0 if no snapshot is available; the signal price will be used as-is.
func (om *OrderManager) Execute(ctx context.Context, sig strategy.Signal, slotLabel string, marketPrice float64) *OrderResult {
	execPrice := sig.Price

	if marketPrice > 0 {
		// Slippage check: reject if market moved too far from signal.
		if sig.Price > 0 {
			deviation := math.Abs(marketPrice-sig.Price) / sig.Price
			slog.Debug("slippage check",
				"slot", slotLabel, "signal_price", fmt.Sprintf("%.4f", sig.Price),
				"market_price", fmt.Sprintf("%.4f", marketPrice),
				"deviation", fmt.Sprintf("%.2f%%", deviation*100))
			if deviation > om.maxSlippagePct {
				slog.Warn("slippage rejected",
					"slot", slotLabel, "side", sig.Side,
					"signal_price", fmt.Sprintf("%.4f", sig.Price),
					"market_price", fmt.Sprintf("%.4f", marketPrice),
					"deviation", fmt.Sprintf("%.2f%%", deviation*100))
				return &OrderResult{Success: false, ErrorMsg: "slippage exceeded"}
			}
		}
		// Use market price for FAK so the order fills immediately.
		execPrice = marketPrice

		// Apply price buffer for BUY FAK orders to compensate for orderbook
		// staleness. Cap at EntryLimit so we never exceed the strategy's ceiling.
		if sig.Side == clob.SideBuy {
			tickSize := parseFloat(sig.TickSize)
			if tickSize > 0 {
				buffer := float64(om.fakPriceBufferTicks) * tickSize
				buffered := execPrice + buffer
				if sig.EntryLimit > 0 && buffered > sig.EntryLimit {
					buffered = sig.EntryLimit
				}
				if buffered <= 1.0 && buffered > execPrice {
					slog.Debug("fak price buffer applied",
						"slot", slotLabel,
						"original", fmt.Sprintf("%.4f", execPrice),
						"buffered", fmt.Sprintf("%.4f", buffered),
						"buffer_ticks", om.fakPriceBufferTicks)
					execPrice = buffered
				}
			}
		}
	} else {
		slog.Warn("no market price",
			"slot", slotLabel,
			"token", truncID(sig.TokenID))
	}

	// Compute order amount.
	// For BUY: prefer MaxCost (strategy's USDC budget intent).
	// Fallback: derive from Price×Size for strategies not providing MaxCost.
	var amount float64
	if sig.Side == clob.SideBuy {
		if sig.MaxCost > 0 {
			amount = sig.MaxCost
		} else {
			amount = sig.Price * sig.Size
		}
	} else {
		amount = sig.Size // shares to sell
	}

	slog.Info("order submitted",
		"slot", slotLabel, "side", sig.Side,
		"price", fmt.Sprintf("%.4f", execPrice),
		"amount", fmt.Sprintf("%.2f", amount),
		"token", truncID(sig.TokenID))

	order, err := om.client.BuildMarketOrder(clob.MarketOrderArgs{
		TokenID:    sig.TokenID,
		Amount:     amount,
		Price:      execPrice,
		Side:       sig.Side,
		FeeRateBps: sig.FeeRateBps,
	}, sig.TickSize, sig.NegRisk)
	if err != nil {
		slog.Error("build order error", "slot", slotLabel, "err", err)
		return &OrderResult{Success: false, ErrorMsg: err.Error()}
	}

	// Use a detached context with timeout so in-flight orders complete
	// even when the parent context is cancelled (e.g., SIGINT shutdown).
	orderCtx, orderCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer orderCancel()

	// Retry loop for transient errors (425 Too Early, 429 Rate Limit, 500 Server Error).
	var resp *clob.OrderResponse
	for attempt := range maxOrderRetries {
		resp, err = om.client.PlaceOrder(orderCtx, order, clob.OrderTypeFAK)
		if err == nil {
			break
		}
		if !clob.IsRetryableError(err) {
			break
		}
		backoff := time.Duration(attempt+1) * 2 * time.Second
		slog.Warn("order transient retry",
			"slot", slotLabel, "attempt", fmt.Sprintf("%d/%d", attempt+1, maxOrderRetries),
			"backoff", backoff, "err", err)
		time.Sleep(backoff)
	}
	if err != nil {
		slog.Error("order failed",
			"slot", slotLabel, "side", sig.Side,
			"price", fmt.Sprintf("%.4f", execPrice),
			"amount", fmt.Sprintf("%.2f", amount),
			"err", err)
		return &OrderResult{Success: false, ErrorMsg: err.Error()}
	}

	args := []any{
		"slot", slotLabel, "side", sig.Side,
		"price", fmt.Sprintf("%.4f", execPrice),
		"amount", fmt.Sprintf("%.2f", amount),
		"order_id", resp.OrderID, "status", resp.Status,
	}
	if !resp.Success {
		args = append(args, "err", resp.ErrorMsg)
	}
	slog.Info("order result", args...)

	return &OrderResult{
		Success:  resp.Success,
		OrderID:  resp.OrderID,
		Status:   resp.Status,
		ErrorMsg: resp.ErrorMsg,
	}
}

// MarketPrice extracts the current execution price for a signal from the snapshot store.
// Returns the best ask (for BUY) or best bid (for SELL), or 0 if unavailable.
func MarketPrice(snapshots *ws.SnapshotStore, tokenID, side string) float64 {
	snap := snapshots.Get(tokenID)
	if snap.BestAsk == "" && snap.BestBid == "" {
		return 0
	}
	if side == clob.SideBuy {
		return parseFloat(snap.BestAsk)
	}
	return parseFloat(snap.BestBid)
}
