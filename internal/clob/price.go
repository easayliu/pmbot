package clob

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// Side represents the order side.
const (
	SideBuy  = "BUY"
	SideSell = "SELL"
)

// GetPrice retrieves the price for a single token and side.
func (c *Client) GetPrice(ctx context.Context, tokenID, side string) (*PriceResponse, error) {
	params := url.Values{
		"token_id": {tokenID},
		"side":     {side},
	}
	var resp PriceResponse
	if err := c.get(ctx, "/price", params, &resp); err != nil {
		return nil, fmt.Errorf("get price: %w", err)
	}
	return &resp, nil
}

// GetMidpoint retrieves the midpoint price for a single token.
func (c *Client) GetMidpoint(ctx context.Context, tokenID string) (*MidpointResponse, error) {
	params := url.Values{"token_id": {tokenID}}
	var resp MidpointResponse
	if err := c.get(ctx, "/midpoint", params, &resp); err != nil {
		return nil, fmt.Errorf("get midpoint: %w", err)
	}
	return &resp, nil
}

// GetSpread retrieves the bid-ask spread for a single token.
func (c *Client) GetSpread(ctx context.Context, tokenID string) (*SpreadResponse, error) {
	params := url.Values{"token_id": {tokenID}}
	var resp SpreadResponse
	if err := c.get(ctx, "/spread", params, &resp); err != nil {
		return nil, fmt.Errorf("get spread: %w", err)
	}
	return &resp, nil
}

// GetLastTradePrice retrieves the last trade price for a single token.
func (c *Client) GetLastTradePrice(ctx context.Context, tokenID string) (*LastTradePriceResponse, error) {
	params := url.Values{"token_id": {tokenID}}
	var resp LastTradePriceResponse
	if err := c.get(ctx, "/last-trade-price", params, &resp); err != nil {
		return nil, fmt.Errorf("get last trade price: %w", err)
	}
	return &resp, nil
}

// PriceHistoryParams contains parameters for the price history endpoint.
type PriceHistoryParams struct {
	TokenID  string
	StartTs  *int64
	EndTs    *int64
	Interval string // "1m", "1w", "1d", "6h", "1h", "max"
	Fidelity *int   // Resolution in minutes
}

// GetPriceHistory retrieves historical prices for a token.
func (c *Client) GetPriceHistory(ctx context.Context, params PriceHistoryParams) (*PriceHistoryResponse, error) {
	q := url.Values{"market": {params.TokenID}}
	if params.StartTs != nil {
		q.Set("startTs", strconv.FormatInt(*params.StartTs, 10))
	}
	if params.EndTs != nil {
		q.Set("endTs", strconv.FormatInt(*params.EndTs, 10))
	}
	if params.Interval != "" {
		q.Set("interval", params.Interval)
	}
	if params.Fidelity != nil {
		q.Set("fidelity", strconv.Itoa(*params.Fidelity))
	}

	var resp PriceHistoryResponse
	if err := c.get(ctx, "/prices-history", q, &resp); err != nil {
		return nil, fmt.Errorf("get price history: %w", err)
	}
	return &resp, nil
}
