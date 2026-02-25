package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const rtdsWSURL = "wss://ws-live-data.polymarket.com"

const (
	rtdsReadDeadline      = 5 * time.Second // Layer 1: total silence → reconnect
	chainlinkStaleTimeout = 3 * time.Second // Layer 2: Chainlink silent → reconnect
)

// rtdsMessage is the envelope for all RTDS WebSocket messages.
type rtdsMessage struct {
	Topic     string          `json:"topic"`
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// rtdsPricePayload is the payload for crypto price updates.
type rtdsPricePayload struct {
	Symbol    string  `json:"symbol"`
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

// rtdsSubscribeMsg is the subscription request format.
type rtdsSubscribeMsg struct {
	Action        string             `json:"action"`
	Subscriptions []rtdsSubscription `json:"subscriptions"`
}

type rtdsSubscription struct {
	Topic   string `json:"topic"`
	Type    string `json:"type"`
	Filters string `json:"filters"`
}

// RTDSFeed streams real-time Chainlink BTC/USD prices from Polymarket's
// Real-Time Data Socket (RTDS). This is the exact data source Polymarket
// uses to resolve 5-minute Up/Down markets.
type RTDSFeed struct {
	symbol            string // e.g., "btc/usd"
	ticksCh           chan PriceTick
	lastChainlinkTick time.Time // time of last Chainlink price message
	lastBinanceTick   time.Time // time of last Binance price message (health probe)
}

// NewRTDSFeed creates a feed that connects to Polymarket RTDS for Chainlink prices.
func NewRTDSFeed(symbol string) *RTDSFeed {
	return &RTDSFeed{
		symbol:  symbol,
		ticksCh: make(chan PriceTick, 256),
	}
}

// Ticks returns the channel that receives price ticks.
func (f *RTDSFeed) Ticks() <-chan PriceTick {
	return f.ticksCh
}

// Run connects to Polymarket RTDS and streams Chainlink prices until ctx is cancelled.
// It automatically reconnects with exponential backoff on disconnection.
func (f *RTDSFeed) Run(ctx context.Context) {
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
			slog.Warn("rtds ws error, reconnecting", "err", err, "backoff", backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		if err != nil && strings.HasPrefix(err.Error(), "dial:") {
			// Dial failure: server is unreachable. Use exponential
			// backoff to avoid hammering a down server.
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			// Connection was established but failed (stale, read error).
			// Reset backoff for fast reconnect — the server is alive.
			backoff = 1 * time.Second
		}
	}
}

// stream establishes a single WebSocket session and reads price updates.
// It subscribes to both crypto_prices_chainlink (primary data) and
// crypto_prices (Binance probe) to detect Chainlink-specific stalls
// even when the connection is alive.
func (f *RTDSFeed) stream(ctx context.Context) error {
	slog.Info("rtds ws connecting", "url", rtdsWSURL, "symbol", f.symbol)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, rtdsWSURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// Subscribe to Chainlink (primary) + Binance (health probe).
	chainlinkFilters, _ := json.Marshal(map[string]string{"symbol": f.symbol})
	binanceFilters, _ := json.Marshal(map[string]string{"symbol": "btcusdt"})
	sub := rtdsSubscribeMsg{
		Action: "subscribe",
		Subscriptions: []rtdsSubscription{
			{Topic: "crypto_prices_chainlink", Type: "*", Filters: string(chainlinkFilters)},
			{Topic: "crypto_prices", Type: "*", Filters: string(binanceFilters)},
		},
	}
	if err := conn.WriteJSON(sub); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	slog.Info("rtds ws connected", "primary", "crypto_prices_chainlink", "probe", "crypto_prices", "symbol", f.symbol)

	// Layer 1: ReadDeadline — detect total connection death.
	if err := conn.SetReadDeadline(time.Now().Add(rtdsReadDeadline)); err != nil {
		return fmt.Errorf("set initial read deadline: %w", err)
	}

	// Reset per-session tracking timestamps.
	f.lastChainlinkTick = time.Time{}
	f.lastBinanceTick = time.Time{}

	// Start ping goroutine to keep connection alive.
	// gorilla/websocket supports one concurrent reader and one concurrent writer.
	pingCtx, pingCancel := context.WithCancel(ctx)
	defer pingCancel()
	go f.pingLoop(pingCtx, conn)

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

		// Extend read deadline on every successful read.
		if err := conn.SetReadDeadline(time.Now().Add(rtdsReadDeadline)); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}

		var envelope rtdsMessage
		if err := json.Unmarshal(msg, &envelope); err != nil {
			slog.Debug("rtds unmarshal error", "err", err)
			continue
		}

		switch envelope.Topic {
		case "crypto_prices_chainlink":
			f.lastChainlinkTick = time.Now()

			var payload rtdsPricePayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				slog.Debug("rtds payload unmarshal error", "err", err)
				continue
			}

			if payload.Value <= 0 || math.IsNaN(payload.Value) || math.IsInf(payload.Value, 0) {
				continue
			}

			tick := PriceTick{
				Symbol: payload.Symbol,
				Price:  payload.Value,
				Time:   time.UnixMilli(payload.Timestamp),
			}

			select {
			case f.ticksCh <- tick:
			default:
				// Drop oldest, push newest.
				<-f.ticksCh
				f.ticksCh <- tick
			}

		case "crypto_prices":
			f.lastBinanceTick = time.Now()
			// Binance is a pure health probe — do not emit ticks.

		default:
			continue
		}

		// Layer 2: detect Chainlink-specific stall while connection is alive.
		// If Binance messages keep arriving but Chainlink is silent beyond
		// the threshold, force a reconnect.
		if !f.lastChainlinkTick.IsZero() && time.Since(f.lastChainlinkTick) > chainlinkStaleTimeout {
			return fmt.Errorf("chainlink stale for %.1fs, forcing reconnect", time.Since(f.lastChainlinkTick).Seconds())
		}
	}
}

// pingLoop sends periodic PING messages to keep the WebSocket alive.
func (f *RTDSFeed) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.TextMessage, []byte("PING")); err != nil {
				return
			}
		}
	}
}
