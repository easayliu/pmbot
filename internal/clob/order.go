package clob

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"net/url"
	"strconv"
)

const zeroAddress = "0x0000000000000000000000000000000000000000"

// roundingConfig holds precision settings per tick size (matching py-clob-client).
type roundingConfig struct {
	price  int // decimal places for price
	size   int // decimal places for size (always 2)
	amount int // decimal places for amounts (price + size)
}

// roundingConfigs maps tick_size to its precision settings.
var roundingConfigs = map[string]roundingConfig{
	"0.1":    {price: 1, size: 2, amount: 3},
	"0.01":   {price: 2, size: 2, amount: 4},
	"0.001":  {price: 3, size: 2, amount: 5},
	"0.0001": {price: 4, size: 2, amount: 6},
}

// BuildOrder constructs and signs an order from user-facing arguments.
// The tickSize and negRisk should be obtained from the CLOB API for the given token.
func (c *Client) BuildOrder(args OrderArgs, tickSize string, negRisk bool) (*Order, error) {
	if c.privateKey == nil {
		return nil, ErrNoPrivateKey
	}

	// Generate random salt for order uniqueness.
	salt, err := randomSalt()
	if err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	makerAmount, takerAmount, err := calculateAmounts(args.Side, args.Price, args.Size, tickSize)
	if err != nil {
		return nil, fmt.Errorf("calculate amounts: %w", err)
	}

	order := Order{
		Salt:          json.Number(salt),
		Maker:         c.funder.Hex(),
		Signer:        c.address.Hex(),
		Taker:         zeroAddress,
		TokenID:       args.TokenID,
		MakerAmount:   makerAmount,
		TakerAmount:   takerAmount,
		Expiration:    strconv.FormatInt(args.Expiration, 10),
		Nonce:         "0",
		FeeRateBps:    strconv.Itoa(args.FeeRateBps),
		Side:          args.Side,
		SignatureType: int(c.sigType),
	}

	sig, err := signOrder(c.privateKey, order, c.chainID, negRisk)
	if err != nil {
		return nil, fmt.Errorf("sign order: %w", err)
	}
	order.Signature = sig

	return &order, nil
}

// PlaceOrderOption configures optional parameters for PlaceOrder.
type PlaceOrderOption func(*PlaceOrderRequest)

// WithPostOnly marks the order as post-only (rejected if it would match immediately).
// Only compatible with GTC and GTD order types.
func WithPostOnly() PlaceOrderOption {
	return func(r *PlaceOrderRequest) { r.PostOnly = true }
}

// WithDeferExec defers order execution.
func WithDeferExec() PlaceOrderOption {
	return func(r *PlaceOrderRequest) { r.DeferExec = true }
}

// PlaceOrder submits a signed order to the CLOB API.
func (c *Client) PlaceOrder(ctx context.Context, order *Order, orderType OrderType, opts ...PlaceOrderOption) (*OrderResponse, error) {
	if c.credentials == nil {
		return nil, ErrNoCredentials
	}

	req := PlaceOrderRequest{
		Order:     *order,
		Owner:     c.credentials.Key,
		OrderType: string(orderType),
	}
	for _, opt := range opts {
		opt(&req)
	}

	// Protocol-level order details: DEBUG only (engine layer logs the operational view).
	slog.Debug("place order",
		"side", order.Side, "type", req.OrderType,
		"token", truncID(order.TokenID),
		"maker_amt", order.MakerAmount, "taker_amt", order.TakerAmount,
		"fee_bps", order.FeeRateBps)

	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		if data, err := json.Marshal(req); err == nil {
			slog.Debug("place order payload", "body", string(data))
		}
	}

	var resp OrderResponse
	if err := c.doL2Request(ctx, "POST", "/order", req, &resp); err != nil {
		return nil, fmt.Errorf("place order: %w", err)
	}
	if !resp.Success && resp.ErrorMsg != "" {
		slog.Error("place order api error", "err", resp.ErrorMsg)
	}
	return &resp, nil
}

// CreateAndPlaceOrder builds, signs, and submits a limit order in one step.
func (c *Client) CreateAndPlaceOrder(ctx context.Context, args OrderArgs, tickSize string, negRisk bool, opts ...PlaceOrderOption) (*OrderResponse, error) {
	order, err := c.BuildOrder(args, tickSize, negRisk)
	if err != nil {
		return nil, fmt.Errorf("build order: %w", err)
	}
	return c.PlaceOrder(ctx, order, args.OrderType, opts...)
}

// CreateAndPlaceMarketOrder builds, signs, and submits a market order (FAK) in one step.
func (c *Client) CreateAndPlaceMarketOrder(ctx context.Context, args MarketOrderArgs, tickSize string, negRisk bool, opts ...PlaceOrderOption) (*OrderResponse, error) {
	order, err := c.BuildMarketOrder(args, tickSize, negRisk)
	if err != nil {
		return nil, fmt.Errorf("build market order: %w", err)
	}
	return c.PlaceOrder(ctx, order, OrderTypeFAK, opts...)
}

// BuildMarketOrder constructs and signs a market order (for FOK/FAK order types).
// For BUY: Amount is dollars to spend, Price is max acceptable price (slippage protection).
// For SELL: Amount is shares to sell, Price is min acceptable price (slippage protection).
func (c *Client) BuildMarketOrder(args MarketOrderArgs, tickSize string, negRisk bool) (*Order, error) {
	if c.privateKey == nil {
		return nil, ErrNoPrivateKey
	}

	salt, err := randomSalt()
	if err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	makerAmount, takerAmount, err := calculateMarketAmounts(args.Side, args.Price, args.Amount, tickSize)
	if err != nil {
		return nil, fmt.Errorf("calculate market amounts: %w", err)
	}

	order := Order{
		Salt:          json.Number(salt),
		Maker:         c.funder.Hex(),
		Signer:        c.address.Hex(),
		Taker:         zeroAddress,
		TokenID:       args.TokenID,
		MakerAmount:   makerAmount,
		TakerAmount:   takerAmount,
		Expiration:    strconv.FormatInt(args.Expiration, 10),
		Nonce:         "0",
		FeeRateBps:    strconv.Itoa(args.FeeRateBps),
		Side:          args.Side,
		SignatureType: int(c.sigType),
	}

	sig, err := signOrder(c.privateKey, order, c.chainID, negRisk)
	if err != nil {
		return nil, fmt.Errorf("sign order: %w", err)
	}
	order.Signature = sig

	return &order, nil
}

// PlaceOrders submits multiple signed orders in a single batch request (max 15).
func (c *Client) PlaceOrders(ctx context.Context, items []PlaceOrdersItem) ([]OrderResponse, error) {
	if c.credentials == nil {
		return nil, ErrNoCredentials
	}
	if len(items) > 15 {
		return nil, fmt.Errorf("max 15 orders per batch request, got %d", len(items))
	}

	slog.Info("place orders batch", "count", len(items))

	var resp []OrderResponse
	if err := c.doL2Request(ctx, "POST", "/orders", items, &resp); err != nil {
		return nil, fmt.Errorf("place orders: %w", err)
	}
	return resp, nil
}

// PostHeartbeat sends a heartbeat to keep the session alive and prevent
// automatic cancellation of open orders (10-second timeout with 5-second buffer).
func (c *Client) PostHeartbeat(ctx context.Context, heartbeatID string) (*HeartbeatResponse, error) {
	if c.credentials == nil {
		return nil, ErrNoCredentials
	}

	body := map[string]string{"heartbeat_id": heartbeatID}
	var resp HeartbeatResponse
	if err := c.doL2Request(ctx, "POST", "/heartbeat", body, &resp); err != nil {
		return nil, fmt.Errorf("post heartbeat: %w", err)
	}
	return &resp, nil
}

// CancelOrder cancels a single order by ID.
func (c *Client) CancelOrder(ctx context.Context, orderID string) (*CancelResponse, error) {
	body := map[string]string{"orderID": orderID}
	var resp CancelResponse
	if err := c.doL2Request(ctx, "DELETE", "/order", body, &resp); err != nil {
		return nil, fmt.Errorf("cancel order %s: %w", orderID, err)
	}
	return &resp, nil
}

// CancelAll cancels all open orders.
func (c *Client) CancelAll(ctx context.Context) (*CancelResponse, error) {
	var resp CancelResponse
	if err := c.doL2Request(ctx, "DELETE", "/cancel-all", nil, &resp); err != nil {
		return nil, fmt.Errorf("cancel all orders: %w", err)
	}
	return &resp, nil
}

// GetOpenOrders retrieves open orders, optionally filtered by market or asset.
func (c *Client) GetOpenOrders(ctx context.Context, market, assetID string) (*OpenOrdersResponse, error) {
	// Build the query path with params for HMAC signing.
	q := url.Values{}
	if market != "" {
		q.Set("market", market)
	}
	if assetID != "" {
		q.Set("asset_id", assetID)
	}
	path := "/data/orders"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	var resp OpenOrdersResponse
	if err := c.doL2Request(ctx, "GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("get open orders: %w", err)
	}
	return &resp, nil
}

// GetTrades retrieves trade history, optionally filtered by market or asset.
func (c *Client) GetTrades(ctx context.Context, market, assetID string) (*TradesResponse, error) {
	q := url.Values{}
	if market != "" {
		q.Set("market", market)
	}
	if assetID != "" {
		q.Set("asset_id", assetID)
	}
	path := "/data/trades"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	var resp TradesResponse
	if err := c.doL2Request(ctx, "GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("get trades: %w", err)
	}
	return &resp, nil
}

// GetFeeRate retrieves the base fee rate (in basis points) for a given token.
func (c *Client) GetFeeRate(ctx context.Context, tokenID string) (int, error) {
	params := url.Values{"token_id": {tokenID}}
	var resp struct {
		BaseFee int `json:"base_fee"`
	}
	if err := c.get(ctx, "/fee-rate", params, &resp); err != nil {
		return 0, fmt.Errorf("get fee rate: %w", err)
	}
	return resp.BaseFee, nil
}

// GetTickSize retrieves the tick size for a given token from the CLOB API.
func (c *Client) GetTickSize(ctx context.Context, tokenID string) (string, error) {
	params := url.Values{"token_id": {tokenID}}
	var resp struct {
		MinimumTickSize float64 `json:"minimum_tick_size"`
	}
	if err := c.get(ctx, "/tick-size", params, &resp); err != nil {
		return "", fmt.Errorf("get tick size: %w", err)
	}
	return strconv.FormatFloat(resp.MinimumTickSize, 'f', -1, 64), nil
}

// GetNegRisk retrieves whether a token is in a neg-risk market.
func (c *Client) GetNegRisk(ctx context.Context, tokenID string) (bool, error) {
	params := url.Values{"token_id": {tokenID}}
	var resp struct {
		NegRisk bool `json:"neg_risk"`
	}
	if err := c.get(ctx, "/neg-risk", params, &resp); err != nil {
		return false, fmt.Errorf("get neg risk: %w", err)
	}
	return resp.NegRisk, nil
}

// calculateMarketAmounts computes makerAmount and takerAmount for market orders (FOK/FAK).
//
// For BUY: amount is dollars to spend, price is max price (slippage protection).
//   - makerAmount = amount (USDC), takerAmount = amount/price (tokens)
//
// For SELL: amount is shares to sell, price is min price (slippage protection).
//   - makerAmount = amount (tokens), takerAmount = amount*price (USDC)
//
// Uses standard rounding (not floor) with amount precision from tick size config.
func calculateMarketAmounts(side string, price, amount float64, tickSize string) (string, string, error) {
	if price <= 0 || price > 1.0 {
		return "", "", fmt.Errorf("price out of range (0, 1]: %f", price)
	}
	if amount <= 0 {
		return "", "", fmt.Errorf("amount must be positive: %f", amount)
	}

	cfg, ok := roundingConfigs[tickSize]
	if !ok {
		return "", "", fmt.Errorf("unsupported tick size: %s", tickSize)
	}

	var rawMaker, rawTaker float64
	var makerPrec, takerPrec int
	switch side {
	case SideBuy:
		rawMaker = amount         // USDC to spend
		rawTaker = amount / price // Tokens to receive
		makerPrec = cfg.amount    // USDC uses amount precision
		takerPrec = cfg.size      // tokens use size precision (2 decimals)
	case SideSell:
		rawMaker = amount         // Tokens to sell
		rawTaker = amount * price // USDC to receive
		makerPrec = cfg.size      // tokens use size precision (2 decimals)
		takerPrec = cfg.amount    // USDC uses amount precision
	default:
		return "", "", fmt.Errorf("invalid side: %s", side)
	}

	makerAmount := toTokenDecimals(roundNormal(rawMaker, makerPrec))
	takerAmount := toTokenDecimals(roundNormal(rawTaker, takerPrec))

	return makerAmount, takerAmount, nil
}

// calculateAmounts computes makerAmount and takerAmount as on-chain token values (6 decimals).
//
// Matching py-clob-client logic:
//  1. Round the token amount (size) DOWN to 2 decimal places.
//  2. Derive the USDC amount as roundedSize × price so the ratio is exact.
//  3. Convert both to 6-decimal fixed-point integers.
func calculateAmounts(side string, price, size float64, tickSize string) (string, string, error) {
	if price <= 0 || price > 1.0 {
		return "", "", fmt.Errorf("price out of range (0, 1]: %f", price)
	}
	if size <= 0 {
		return "", "", fmt.Errorf("size must be positive: %f", size)
	}

	cfg, ok := roundingConfigs[tickSize]
	if !ok {
		return "", "", fmt.Errorf("unsupported tick size: %s", tickSize)
	}

	// Round the token side (size) DOWN to size precision (always 2 decimals).
	roundedSize := roundDown(size, cfg.size)
	if roundedSize <= 0 {
		return "", "", fmt.Errorf("size too small after rounding: %f -> %f", size, roundedSize)
	}

	// Derive the USDC amount from the rounded token amount so
	// makerAmount/takerAmount ratio equals the exact price.
	var rawMaker, rawTaker float64
	switch side {
	case SideBuy:
		rawTaker = roundedSize         // Tokens to receive
		rawMaker = roundedSize * price // USDC to pay
	case SideSell:
		rawMaker = roundedSize         // Tokens to sell
		rawTaker = roundedSize * price // USDC to receive
	default:
		return "", "", fmt.Errorf("invalid side: %s", side)
	}

	makerAmount := toTokenDecimals(rawMaker)
	takerAmount := toTokenDecimals(rawTaker)

	return makerAmount, takerAmount, nil
}

// toTokenDecimals converts a float amount to the on-chain integer representation (6 decimals).
// Uses math.Round (matching py-clob-client) instead of truncation.
func toTokenDecimals(amount float64) string {
	return strconv.FormatInt(int64(math.Round(amount*1e6)), 10)
}

// roundDown floors a float to the specified number of decimal places (matching py-clob-client round_down).
func roundDown(val float64, decimals int) float64 {
	shift := pow10(decimals)
	return math.Floor(val*shift) / shift
}

// roundNormal rounds a float to the specified number of decimal places using standard rounding.
// Used for market order amount calculations (matching py-clob-client round_normal).
func roundNormal(val float64, decimals int) float64 {
	shift := pow10(decimals)
	return math.Round(val*shift) / shift
}

func pow10(n int) float64 {
	result := 1.0
	for i := 0; i < n; i++ {
		result *= 10
	}
	return result
}

// randomSalt generates a random 48-bit salt as a decimal string.
// Must stay within JavaScript's Number.MAX_SAFE_INTEGER (2^53-1) so the
// API server preserves exact precision during JSON parsing.
func randomSalt() (string, error) {
	b := make([]byte, 6) // 48 bits → max ~2.8×10^14, safely under 2^53
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return new(big.Int).SetBytes(b).String(), nil
}
