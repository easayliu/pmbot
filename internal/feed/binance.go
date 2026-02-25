package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const binanceWSURL = "wss://stream.binance.com:9443/ws"

// binanceAggTrade is the Binance aggregated trade stream message.
type binanceAggTrade struct {
	Symbol string `json:"s"`
	Price  string `json:"p"`
	Time   int64  `json:"T"` // Trade time in ms
}

// BinanceFeed streams real-time trades from Binance via WebSocket.
type BinanceFeed struct {
	symbol  string
	ticksCh chan PriceTick
}

// NewBinanceFeed creates a feed that connects to Binance WebSocket.
// The interval parameter is kept for API compatibility but unused with WS.
func NewBinanceFeed(symbol string, _ time.Duration) *BinanceFeed {
	return &BinanceFeed{
		symbol:  symbol,
		ticksCh: make(chan PriceTick, 256),
	}
}

// Ticks returns the channel that receives price ticks.
func (f *BinanceFeed) Ticks() <-chan PriceTick {
	return f.ticksCh
}

// Run connects to Binance WebSocket and streams trades until ctx is cancelled.
// It automatically reconnects with exponential backoff on disconnection.
func (f *BinanceFeed) Run(ctx context.Context) {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := f.stream(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("binance ws error, reconnecting", "err", err, "backoff", backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// stream establishes a single WebSocket session and reads trades.
func (f *BinanceFeed) stream(ctx context.Context) error {
	// Binance stream name: <symbol>@aggTrade (lowercase)
	streamName := strings.ToLower(f.symbol) + "@aggTrade"
	url := fmt.Sprintf("%s/%s", binanceWSURL, streamName)

	slog.Info("binance ws connecting", "url", url)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	slog.Info("binance ws connected", "stream", streamName)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var trade binanceAggTrade
		if err := json.Unmarshal(msg, &trade); err != nil {
			slog.Warn("binance ws unmarshal error", "err", err)
			continue
		}

		price, err := strconv.ParseFloat(trade.Price, 64)
		if err != nil || math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
			continue
		}

		tick := PriceTick{
			Symbol: trade.Symbol,
			Price:  price,
			Time:   time.UnixMilli(trade.Time),
		}

		select {
		case f.ticksCh <- tick:
		default:
			// Drop oldest, push newest.
			<-f.ticksCh
			f.ticksCh <- tick
		}
	}
}

// binanceTickerResp is the Binance REST API response for /api/v3/ticker/price.
type binanceTickerResp struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

// FetchSpotPrice queries Binance REST API for the current spot price.
// Used at startup to seed the price before WebSocket ticks arrive.
func FetchSpotPrice(ctx context.Context, symbol string) (float64, error) {
	url := fmt.Sprintf("https://api.binance.com/api/v3/ticker/price?symbol=%s", symbol)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}

	var ticker binanceTickerResp
	if err := json.NewDecoder(resp.Body).Decode(&ticker); err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}

	price, err := strconv.ParseFloat(ticker.Price, 64)
	if err != nil || math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
		return 0, fmt.Errorf("invalid price %q", ticker.Price)
	}

	return price, nil
}
