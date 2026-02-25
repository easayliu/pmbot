package ws

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
)

// Event types from Polymarket WebSocket.
const (
	EventBook           = "book"
	EventPriceChange    = "price_change"
	EventLastTradePrice = "last_trade_price"
	EventBestBidAsk     = "best_bid_ask"
	EventTickSizeChange = "tick_size_change"
	EventMarketResolved = "market_resolved"
)

// MarketEvent is a flat union of all WS event fields.
// Only a subset of fields will be populated depending on the event type.
//
// price_change events are flattened by the Client before delivery: each
// element in the server's price_changes[] array becomes a separate
// MarketEvent with top-level AssetID, Price, BestBid, and BestAsk.
type MarketEvent struct {
	EventType string `json:"event_type"`
	AssetID   string `json:"asset_id"`

	// book event
	Market    string       `json:"market,omitempty"`
	Timestamp string       `json:"timestamp,omitempty"`
	Bids      []OrderLevel `json:"bids,omitempty"`
	Asks      []OrderLevel `json:"asks,omitempty"`

	// price_change (raw from server; flattened by Client before delivery)
	PriceChanges []PriceChangeEntry `json:"price_changes,omitempty"`

	// price_change (flattened) / last_trade_price
	Price string `json:"price,omitempty"`
	Side  string `json:"side,omitempty"`

	// best_bid_ask / price_change (flattened)
	BestBid string `json:"best_bid,omitempty"`
	BestAsk string `json:"best_ask,omitempty"`

	// tick_size_change
	TickSize string `json:"tick_size,omitempty"`

	// market_resolved
	AssetsIDs      []string `json:"assets_ids,omitempty"`
	Outcomes       []string `json:"outcomes,omitempty"`
	WinningAssetID string   `json:"winning_asset_id,omitempty"`
	WinningOutcome string   `json:"winning_outcome,omitempty"`
}

// PriceChangeEntry is a single element in a price_change event's
// price_changes[] array. The Client flattens these into individual
// MarketEvent objects before sending them through the events channel.
type PriceChangeEntry struct {
	AssetID string `json:"asset_id"`
	Price   string `json:"price"`
	Size    string `json:"size"`
	Side    string `json:"side"`
	BestBid string `json:"best_bid"`
	BestAsk string `json:"best_ask"`
}

// OrderLevel represents a price/size pair in the order book.
type OrderLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// AssetData holds the latest WS data for a single asset (token).
type AssetData struct {
	Bids      []OrderLevel
	Asks      []OrderLevel
	BestBid   string
	BestAsk   string
	LastPrice string
}

// SnapshotStore tracks per-asset market data from WS events.
type SnapshotStore struct {
	mu    sync.RWMutex
	items map[string]*AssetData // keyed by asset_id (token ID)
}

// NewSnapshotStore creates an empty SnapshotStore.
func NewSnapshotStore() *SnapshotStore {
	return &SnapshotStore{items: make(map[string]*AssetData)}
}

// Reset clears all stored snapshots without reallocating the map.
func (s *SnapshotStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.items)
}

// Update applies a MarketEvent to the corresponding asset's data.
// Events without a top-level AssetID (e.g., market_resolved) are ignored;
// the engine handles those directly.
func (s *SnapshotStore) Update(e MarketEvent) {
	if e.AssetID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.items[e.AssetID]
	if !ok {
		d = &AssetData{}
		s.items[e.AssetID] = d
	}

	switch e.EventType {
	case EventBook:
		if len(e.Bids) > 0 {
			d.Bids = e.Bids
			d.BestBid = bestPrice(e.Bids, true)
		}
		if len(e.Asks) > 0 {
			d.Asks = e.Asks
			d.BestAsk = bestPrice(e.Asks, false)
		}
		slog.Debug("ws book snapshot",
			"asset", fmt.Sprintf("%.8s", e.AssetID),
			"best_bid", d.BestBid, "best_ask", d.BestAsk,
			"bids", len(e.Bids), "asks", len(e.Asks))
	case EventBestBidAsk:
		if e.BestBid != "" {
			d.BestBid = e.BestBid
		}
		if e.BestAsk != "" {
			d.BestAsk = e.BestAsk
		}
	case EventPriceChange:
		// After Client flattening, price_change events have top-level
		// AssetID, Price, BestBid, and BestAsk.
		if e.Price != "" {
			d.LastPrice = e.Price
		}
		if e.BestBid != "" {
			d.BestBid = e.BestBid
		}
		if e.BestAsk != "" {
			d.BestAsk = e.BestAsk
		}
	case EventLastTradePrice:
		if e.Price != "" {
			d.LastPrice = e.Price
		}
	}
}

// Has returns true if the store has received bid/ask data for the asset.
// Returns false if only LastPrice is set (e.g., from last_trade_price events)
// since BestBid/BestAsk are required for strategy evaluation.
func (s *SnapshotStore) Has(assetID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.items[assetID]
	if !ok {
		return false
	}
	return d.BestBid != "" || d.BestAsk != ""
}

// Get returns a copy of the asset data for the given token ID.
func (s *SnapshotStore) Get(assetID string) AssetData {
	s.mu.RLock()
	defer s.mu.RUnlock()

	d, ok := s.items[assetID]
	if !ok {
		return AssetData{}
	}
	return AssetData{
		Bids:      append([]OrderLevel(nil), d.Bids...),
		Asks:      append([]OrderLevel(nil), d.Asks...),
		BestBid:   d.BestBid,
		BestAsk:   d.BestAsk,
		LastPrice: d.LastPrice,
	}
}

// bestPrice extracts the best price from an order level slice.
// For bids (highest=true), returns the highest price.
// For asks (highest=false), returns the lowest price.
func bestPrice(levels []OrderLevel, highest bool) string {
	if len(levels) == 0 {
		return ""
	}
	best := ""
	bestVal := 0.0
	if !highest {
		bestVal = math.MaxFloat64 // for asks, start high so any valid price wins
	}
	for _, l := range levels {
		v := parseFloat(l.Price)
		if v == 0 {
			continue
		}
		if highest && v > bestVal {
			best = l.Price
			bestVal = v
		} else if !highest && v < bestVal {
			best = l.Price
			bestVal = v
		}
	}
	return best
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// SubscribeMessage is the payload sent to subscribe to market channels.
// CustomFeatureEnabled must be true to receive best_bid_ask, new_market,
// and market_resolved events from the Polymarket WebSocket.
type SubscribeMessage struct {
	Type                 string   `json:"type"`
	Markets              []string `json:"markets,omitempty"`
	Assets               []string `json:"assets_ids,omitempty"`
	CustomFeatureEnabled bool     `json:"custom_feature_enabled,omitempty"`
}
