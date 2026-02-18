package engine

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/easay/pmbot/internal/clob"
)

const (
	claimStartDelay   = 30 * time.Second
	claimPollInterval = 2 * time.Minute
	maxClaimRetries   = 3 // max self-healing retries per condition before giving up
)

// conditionGroup aggregates token holdings by conditionID for batch redeem.
type conditionGroup struct {
	conditionID string
	eventSlug   string
	title       string
	negRisk     bool
	// Per-outcome amounts in display units (actual token balance from /positions API).
	yesAmount float64
	noAmount  float64
}

// claimLoop periodically polls for claimable positions and redeems them via relay.
// Authenticates using Polymarket session cookies (same as website, no quota).
func (e *Engine) claimLoop(ctx context.Context) {
	if !e.clobClient.HasSessionCredentials() {
		slog.Info("claim: no session credentials configured, claim loop disabled")
		return
	}

	// Startup delay to let the engine settle.
	select {
	case <-ctx.Done():
		return
	case <-time.After(claimStartDelay):
	}

	slog.Info("claim: loop started")
	ticker := time.NewTicker(claimPollInterval)
	defer ticker.Stop()

	// Run immediately on start, then on each tick.
	e.claimOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("claim: loop stopped")
			return
		case <-ticker.C:
			e.claimOnce(ctx)
		}
	}
}

// claimOnce finds redeemable positions and redeems them.
// Uses the official /positions?redeemable=true parameter to get only
// positions in resolved markets that can be claimed.
func (e *Engine) claimOnce(ctx context.Context) {
	if e.store == nil {
		return
	}

	// Query both PolymarketAddress and Funder (if different), same as refreshPositions.
	polyAddr := strings.ToLower(e.clobClient.PolymarketAddress().Hex())
	funder := strings.ToLower(e.clobClient.Funder().Hex())

	addrs := []string{polyAddr}
	if funder != polyAddr {
		addrs = append(addrs, funder)
	}

	var positions []dataPosition
	for _, addr := range addrs {
		p := e.fetchRedeemablePositions(ctx, addr)
		slog.Info("claim: fetched redeemable positions", "addr", addr, "count", len(p))
		positions = append(positions, p...)
	}
	if len(positions) == 0 {
		slog.Info("claim: no redeemable positions")
		return
	}

	// Group by conditionID, skip already redeemed.
	groups := make(map[string]*conditionGroup)
	for _, p := range positions {
		if p.ConditionID == "" || p.Size <= 0 {
			slog.Info("claim: skipping invalid position",
				"condition_id", p.ConditionID,
				"size", p.Size,
				"title", p.Title)
			continue
		}
		if e.store.IsRedeemed(p.ConditionID) {
			retries := e.claimRetries[p.ConditionID]
			if retries >= maxClaimRetries {
				// Stop retrying — likely a persistent issue (gas, contract, etc.).
				slog.Error("claim: max retries reached, skipping until restart",
					"condition_id", truncCondID(p.ConditionID),
					"title", p.Title,
					"retries", retries)
				continue
			}
			// Self-healing: API says still redeemable but store says redeemed.
			// The previous redeem likely failed (e.g. inner GSN call reverted).
			// Remove the stale record so it gets retried.
			slog.Warn("claim: store says redeemed but API says redeemable, clearing stale record",
				"condition_id", truncCondID(p.ConditionID),
				"title", p.Title,
				"retry", retries+1)
			e.claimRetries[p.ConditionID] = retries + 1
			if err := e.store.DeleteRedeem(p.ConditionID); err != nil {
				slog.Error("claim: failed to delete stale redeem record",
					"condition_id", truncCondID(p.ConditionID),
					"err", err)
				continue
			}
		}

		g, ok := groups[p.ConditionID]
		if !ok {
			g = &conditionGroup{
				conditionID: p.ConditionID,
				eventSlug:   p.EventSlug,
				title:       p.Title,
				negRisk:     p.NegativeRisk,
			}
			groups[p.ConditionID] = g
		}
		if p.OutcomeIndex == 0 {
			g.yesAmount += p.Size
		} else {
			g.noAmount += p.Size
		}
	}

	if len(groups) == 0 {
		return
	}

	slog.Info("claim: redeemable positions found", "count", len(groups))

	// Fetch relay address and nonce.
	rp, err := e.clobClient.GetRelayPayload(ctx)
	if err != nil {
		slog.Error("claim: get relay payload failed", "err", err)
		return
	}

	baseNonce, _ := strconv.Atoi(rp.Nonce)

	slog.Info("claim: relay payload",
		"relay", rp.Address,
		"nonce", baseNonce,
		"conditions", len(groups))

	// Submit all redeems, then verify in batch after on-chain settlement.
	submitted := make(map[string]submittedRedeem, len(groups))
	idx := 0
	for _, g := range groups {
		req := clob.RedeemRequest{
			ConditionID: g.conditionID,
			NegRisk:     g.negRisk,
		}
		if g.negRisk {
			req.Amounts = [2]*big.Int{
				displayToOnChain(g.yesAmount),
				displayToOnChain(g.noAmount),
			}
		}

		nonce := fmt.Sprintf("%d", baseNonce+idx)
		idx++

		slog.Info("claim: redeeming",
			"condition_id", truncCondID(g.conditionID),
			"title", g.title,
			"neg_risk", g.negRisk,
			"yes_amount", g.yesAmount,
			"no_amount", g.noAmount,
			"nonce", nonce)

		// Rate limit: relay enforces ~1 req/s; wait between submissions.
		if idx > 1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1200 * time.Millisecond):
			}
		}

		redeemCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		resp, err := e.clobClient.RedeemWithNonce(redeemCtx, []clob.RedeemRequest{req}, nonce, rp.Address)
		cancel()

		if err != nil {
			slog.Error("claim: redeem failed",
				"condition_id", truncCondID(g.conditionID),
				"err", err)
			continue
		}

		slog.Info("claim: redeem submitted",
			"condition_id", truncCondID(g.conditionID),
			"tx_id", resp.TransactionID,
			"tx_hash", resp.TransactionHash,
			"polygonscan", "https://polygonscan.com/tx/"+resp.TransactionHash)

		submitted[g.conditionID] = submittedRedeem{
			txID:      resp.TransactionID,
			txHash:    resp.TransactionHash,
			eventSlug: g.eventSlug,
		}
	}

	if len(submitted) == 0 {
		return
	}

	// Wait for Polymarket to process the redeems (on-chain + indexer).
	slog.Info("claim: waiting for Polymarket confirmation", "submitted", len(submitted))
	select {
	case <-ctx.Done():
		return
	case <-time.After(90 * time.Second):
	}

	// Verify: positions no longer redeemable means Polymarket has processed the redeem.
	stillRedeemable := make(map[string]bool)
	for _, addr := range []string{
		strings.ToLower(e.clobClient.PolymarketAddress().Hex()),
		strings.ToLower(e.clobClient.Funder().Hex()),
	} {
		for _, p := range e.fetchRedeemablePositions(ctx, addr) {
			if p.ConditionID != "" {
				stillRedeemable[p.ConditionID] = true
			}
		}
	}

	for condID, s := range submitted {
		if stillRedeemable[condID] {
			slog.Warn("claim: redeem not confirmed by Polymarket, will retry next cycle",
				"condition_id", truncCondID(condID),
				"polygonscan", "https://polygonscan.com/tx/"+s.txHash)
			continue
		}

		slog.Info("claim: redeem confirmed",
			"condition_id", truncCondID(condID),
			"tx_hash", s.txHash,
			"polygonscan", "https://polygonscan.com/tx/"+s.txHash)

		if err := e.store.InsertRedeem(condID, s.txHash, s.eventSlug); err != nil {
			slog.Error("claim: insert redeem record failed",
				"condition_id", truncCondID(condID),
				"err", err)
		}
	}
}

// submittedRedeem tracks a submitted relay transaction.
type submittedRedeem struct {
	txID      string // relay transaction ID (for polling status)
	txHash    string // on-chain transaction hash
	eventSlug string
}

// fetchRedeemablePositions queries /positions?redeemable=true for positions
// in resolved markets that can be claimed.
func (e *Engine) fetchRedeemablePositions(ctx context.Context, addr string) []dataPosition {
	u := fmt.Sprintf("%s/positions?user=%s&sizeThreshold=0&redeemable=true", dataAPIBaseURL, addr)
	return e.doFetchPositions(ctx, u, addr)
}

// displayToOnChain converts a display-unit amount (e.g. 10.5 shares) to on-chain units (1e6).
func displayToOnChain(amount float64) *big.Int {
	return big.NewInt(int64(math.Round(amount * 1e6)))
}
