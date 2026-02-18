package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// DefaultURL is the Polymarket CLOB WebSocket endpoint.
	DefaultURL = "wss://ws-subscriptions-clob.polymarket.com/ws/market"

	pingInterval   = 5 * time.Second  // send PING every 5s for faster stale detection
	maxBackoff     = 30 * time.Second
	initialBackoff = 1 * time.Second

	readDeadline      = 10 * time.Second // Layer 1: total silence → reconnect (2x pingInterval)
	eventStaleTimeout = 30 * time.Second // Layer 2: no market events while PONG alive → reconnect
)

// Client manages a WebSocket connection to Polymarket with auto-reconnect.
type Client struct {
	url      string
	assets   []string // token IDs to subscribe
	eventsCh chan MarketEvent
	cancel   context.CancelFunc // set by Run, used by Stop
}

// NewClient creates a new WS client for the given asset IDs.
func NewClient(assetIDs []string) *Client {
	return &Client{
		url:      DefaultURL,
		assets:   assetIDs,
		eventsCh: make(chan MarketEvent, 4096),
	}
}

// Events returns the channel that receives market events.
func (c *Client) Events() <-chan MarketEvent {
	return c.eventsCh
}

// Stop cancels the client's context, causing Run to exit.
func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// Run connects to the WebSocket and processes messages until ctx is cancelled.
// It automatically reconnects on disconnection.
func (c *Client) Run(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	backoff := initialBackoff

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := c.connect(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("ws disconnected, reconnecting", "err", err, "backoff", backoff)
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
			backoff = initialBackoff
		}
	}
}

// connect establishes a single WebSocket session and reads until error.
func (c *Client) connect(ctx context.Context) error {
	slog.Info("ws connecting", "url", c.url)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	// Subscribe before starting heartbeat (no concurrent writes at this point).
	// custom_feature_enabled=true enables best_bid_ask events for real-time
	// top-of-book updates, matching the data shown on the Polymarket UI.
	sub := SubscribeMessage{
		Type:                 "market",
		Assets:               c.assets,
		CustomFeatureEnabled: true,
	}
	if err := conn.WriteJSON(sub); err != nil {
		conn.Close()
		return err
	}
	slog.Info("ws subscribed", "assets", len(c.assets))

	// Layer 1: ReadDeadline — detect total connection death.
	// PONG arrives every ~5s; 10s gives 2x margin.
	if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
		conn.Close()
		return fmt.Errorf("set initial read deadline: %w", err)
	}

	// Layer 2: track last market event time for stale detection.
	subscribeTime := time.Now()
	var lastEventTime time.Time
	var eventCount int

	// Start heartbeat goroutine, tracked by WaitGroup.
	heartCtx, heartCancel := context.WithCancel(ctx)
	var heartWg sync.WaitGroup
	heartWg.Add(1)
	go func() {
		defer heartWg.Done()
		c.heartbeat(heartCtx, conn)
	}()

	// Stop heartbeat before closing connection to prevent concurrent write/close race.
	defer func() {
		heartCancel()
		heartWg.Wait()
		conn.Close()
	}()

	// Read loop.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		// Extend read deadline on every successful read.
		if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}

		// Skip server PONG heartbeat responses.
		if string(msg) == "PONG" {
			// Layer 2: detect market event stall while connection is alive.
			// If PONG keeps arriving but no market data for eventStaleTimeout,
			// force a reconnect to re-subscribe and get fresh book snapshots.
			ref := lastEventTime
			if ref.IsZero() {
				ref = subscribeTime
			}
			if time.Since(ref) > eventStaleTimeout {
				return fmt.Errorf("no market events for %.0fs, forcing reconnect (total_events=%d, uptime=%.0fs)", time.Since(ref).Seconds(), eventCount, time.Since(subscribeTime).Seconds())
			}
			continue
		}

		// Update last market event time for stale detection.
		lastEventTime = time.Now()
		eventCount++

		// The server may send arrays of events.
		var events []MarketEvent
		if err := json.Unmarshal(msg, &events); err != nil {
			// Try single event.
			var single MarketEvent
			if err2 := json.Unmarshal(msg, &single); err2 != nil {
				slog.Warn("ws unmarshal error", "err", err, "raw", truncate(string(msg), 200))
				continue
			}
			events = []MarketEvent{single}
		}

		for _, e := range events {
			if e.EventType == "" {
				continue
			}
			// Flatten price_change events: the server nests per-asset data
			// inside a price_changes[] array with no top-level asset_id.
			// Split each entry into an independent MarketEvent so
			// SnapshotStore can process them by asset_id.
			if e.EventType == EventPriceChange && len(e.PriceChanges) > 0 {
				for _, pc := range e.PriceChanges {
					flat := MarketEvent{
						EventType: EventPriceChange,
						AssetID:   pc.AssetID,
						Price:     pc.Price,
						Side:      pc.Side,
						BestBid:   pc.BestBid,
						BestAsk:   pc.BestAsk,
					}
					select {
					case c.eventsCh <- flat:
					default:
						slog.Warn("ws event channel full, dropping event", "type", flat.EventType)
					}
				}
				continue
			}
			select {
			case c.eventsCh <- e:
			default:
				// Drop if channel full — strategy will catch up.
				slog.Warn("ws event channel full, dropping event", "type", e.EventType)
			}
		}
	}
}

// heartbeat sends text "PING" every pingInterval (Polymarket custom heartbeat).
func (c *Client) heartbeat(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.TextMessage, []byte("PING")); err != nil {
				slog.Warn("ws heartbeat error", "err", err)
				return
			}
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
