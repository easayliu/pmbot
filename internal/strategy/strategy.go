package strategy

import "time"

// Action represents the decision a strategy outputs.
type Action int

const (
	Hold Action = iota
	Buy
	Sell
)

func (a Action) String() string {
	switch a {
	case Buy:
		return "BUY"
	case Sell:
		return "SELL"
	default:
		return "HOLD"
	}
}

// MarketInfo holds metadata and live pricing for a single Polymarket market.
type MarketInfo struct {
	Question    string
	YesTokenID  string
	NoTokenID   string
	TickSize    string
	NegRisk     bool
	FeeRateBps  int     // base fee rate in basis points (from /fee-rate endpoint)
	LowerBound  float64 // range lower bound; 0 means no lower bound
	UpperBound  float64 // range upper bound; 0 means no upper bound
	BestBid   float64 // YES token best bid from WS
	BestAsk   float64 // YES token best ask from WS
	NoBestBid float64 // NO token best bid from WS
	NoBestAsk   float64 // NO token best ask from WS
	YesShares   float64 // YES token shares currently held
	NoShares    float64 // NO token shares currently held
	EntryPrice  float64       // actual fill price of current position (0 = no position)
	EntryElapsed time.Duration // window elapsed at time of entry (for hold-time calculation)
}

// PriceTrend holds BTC price trend metrics computed from recent history.
type PriceTrend struct {
	Change1m  float64 // price change over last 1 minute ($)
	Change5m  float64 // price change over last 5 minutes ($)
	Speed1m   float64 // rate of change: $/min over last 1 minute
	Speed5m   float64 // rate of change: $/min over last 5 minutes
	Volatility float64 // standard deviation of 1-second price deltas over 5 min
	Samples   int     // number of data points in history
}

// IsReady returns true if enough data has been collected for meaningful signals.
// Requires 60 samples (~60s) so Speed1m is based on real 1-minute data, not extrapolated.
func (t PriceTrend) IsReady() bool {
	return t.Samples >= 60
}

// CandleState holds the current and last completed candle directions.
type CandleState struct {
	// Current in-progress window direction.
	Current5m  string // "Up", "Down", or "Unknown"
	Current15m string // "Up", "Down", or "Unknown"
	// Last completed window direction.
	Last5m  string
	Last15m string
	// Price change in current window (close - open).
	Change5m  float64
	Change15m float64
	// Window timing for end-of-window trading.
	Elapsed5m   time.Duration // time elapsed since current 5m window opened
	Remaining5m time.Duration // time remaining until current 5m window closes
	// Recent completed 5m window directions (newest first): "Up", "Down".
	RecentDirs5m []string
	// Partial5m is true when the current 5m candle's open price is unreliable
	// (first tick arrived late, e.g., bot startup mid-window).
	Partial5m bool
}

// MarketState aggregates all data sources for strategy evaluation.
type MarketState struct {
	BTCSpotPrice float64
	Trend        PriceTrend
	Candles      CandleState
	Markets      []MarketInfo
}

// Signal is the output of a strategy evaluation.
type Signal struct {
	Action     Action
	TokenID    string
	Side       string  // "BUY" or "SELL"
	Price      float64 // fill price (market ask at evaluation time)
	Size       float64 // shares = MaxCost / Price
	MaxCost    float64 // USDC budget for this trade (strategy intent)
	TickSize   string
	NegRisk    bool
	FeeRateBps int
	Reason     string

	// Decision metrics: key values used in the buy/hold decision.
	// Populated by strategies so the engine can log them as structured fields.
	Threshold  float64 // vol-adjusted threshold that candle change must exceed
	EntryLimit float64 // max price willing to pay (limit order ceiling); 0 = not set
}

// Strategy is the interface all trading strategies must implement.
type Strategy interface {
	Name() string
	Evaluate(state MarketState) Signal
}
