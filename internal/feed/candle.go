package feed

import (
	"sync"
	"time"
)

// Direction represents the price direction of a candle window.
type Direction string

const (
	DirectionUnknown Direction = "Unknown"
	DirectionUp      Direction = "Up"
	DirectionDown    Direction = "Down"
)

// Candle represents a completed or in-progress price candle for a time window.
type Candle struct {
	Open      float64
	Close     float64
	High      float64
	Low       float64
	Start     time.Time
	End       time.Time
	Direction Direction
	Samples   int
	Partial   bool // true if first tick arrived late (unreliable open price)
}

// partialThreshold is the maximum delay from window start for the first tick
// to be considered a valid open. Beyond this, the candle is marked partial.
// 3 seconds is tight enough to reject unreliable opens while tolerating
// normal RTDS WebSocket jitter (~100-500ms).
const partialThreshold = 3 * time.Second

// currentCandle tracks an in-progress candle.
type currentCandle struct {
	open    float64
	close   float64
	high    float64
	low     float64
	start   time.Time
	samples int
	partial bool // first tick arrived late; open price is unreliable
}

// CandleAggregator builds clock-aligned candles from price ticks.
// Windows are aligned to wall-clock time (e.g., 5-min windows start at :00, :05, :10...).
// Rule: window close price >= open price → Up, otherwise → Down.
type CandleAggregator struct {
	mu      sync.RWMutex
	windows []time.Duration
	candles map[time.Duration]*currentCandle
	history map[time.Duration][]Candle
	maxHist int
}

// NewCandleAggregator creates an aggregator for the given window durations.
// maxHistory controls how many completed candles to retain per window.
func NewCandleAggregator(windows []time.Duration, maxHistory int) *CandleAggregator {
	return &CandleAggregator{
		windows: windows,
		candles: make(map[time.Duration]*currentCandle, len(windows)),
		history: make(map[time.Duration][]Candle, len(windows)),
		maxHist: maxHistory,
	}
}

// AddTick processes a new price tick and updates all tracked windows.
func (ca *CandleAggregator) AddTick(price float64, t time.Time) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	for _, w := range ca.windows {
		windowStart := t.Truncate(w)

		cur, exists := ca.candles[w]
		if !exists || cur.start != windowStart {
			// Close the previous candle. Use the current tick price as close
			// since it is the nearest observation to the window boundary.
			if exists && cur.samples > 0 {
				cur.close = price
				if price > cur.high {
					cur.high = price
				}
				if price < cur.low {
					cur.low = price
				}
				ca.closeCandle(w, cur)
			}
			// Use the current tick price as the new window's open.
			// This matches the Chainlink oracle behaviour: the oracle reads the
			// price at the exact window boundary, and the boundary tick is the
			// closest observation we have to that instant.
			openPrice := price
			// Mark partial if first tick arrived too late after window start.
			// This catches bot startup mid-window and data gaps where the
			// open price is unreliable for direction prediction.
			isPartial := t.Sub(windowStart) > partialThreshold
			// Start a new candle for this window.
			ca.candles[w] = &currentCandle{
				open:    openPrice,
				close:   price,
				high:    openPrice,
				low:     openPrice,
				start:   windowStart,
				samples: 1,
				partial: isPartial,
			}
			continue
		}

		// Update the current in-progress candle.
		cur.close = price
		if price > cur.high {
			cur.high = price
		}
		if price < cur.low {
			cur.low = price
		}
		cur.samples++
	}
}

// closeCandle finalizes a candle and appends it to history.
func (ca *CandleAggregator) closeCandle(w time.Duration, cur *currentCandle) {
	dir := DirectionDown
	if cur.close >= cur.open {
		dir = DirectionUp
	}
	completed := Candle{
		Open:      cur.open,
		Close:     cur.close,
		High:      cur.high,
		Low:       cur.low,
		Start:     cur.start,
		End:       cur.start.Add(w),
		Direction: dir,
		Samples:   cur.samples,
		Partial:   cur.partial,
	}
	ca.history[w] = append(ca.history[w], completed)
	if len(ca.history[w]) > ca.maxHist {
		ca.history[w] = ca.history[w][1:]
	}
}

// SeedHistory injects pre-built completed candles into the history map.
// Candles must be provided oldest-first. This allows LastCompleted() and
// CompletedCandles() to return data immediately after restart.
// Does not affect the current in-progress candle.
func (ca *CandleAggregator) SeedHistory(window time.Duration, candles []Candle) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	for _, c := range candles {
		ca.history[window] = append(ca.history[window], c)
		if len(ca.history[window]) > ca.maxHist {
			ca.history[window] = ca.history[window][1:]
		}
	}
}

// SeedCurrentOpen pre-sets the open price for the current window so the first
// real tick does not mark the candle as partial. Call this during fast restart
// when the previous window's close price is known and the gap is short.
// windowStart must match the current wall-clock window start; if a real tick
// has already initialized the candle, this is a no-op.
func (ca *CandleAggregator) SeedCurrentOpen(window time.Duration, open float64, windowStart time.Time) {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	// Only seed if no candle exists yet for this window.
	if cur, exists := ca.candles[window]; exists && cur.start == windowStart {
		return
	}

	ca.candles[window] = &currentCandle{
		open:    open,
		close:   open,
		high:    open,
		low:     open,
		start:   windowStart,
		samples: 0, // no real ticks yet; first AddTick will update
		partial: false,
	}
}

// CurrentDirection returns the direction of the current in-progress candle.
func (ca *CandleAggregator) CurrentDirection(window time.Duration) Direction {
	ca.mu.RLock()
	defer ca.mu.RUnlock()

	cur, exists := ca.candles[window]
	if !exists || cur.samples == 0 {
		return DirectionUnknown
	}
	if cur.close >= cur.open {
		return DirectionUp
	}
	return DirectionDown
}

// CurrentCandle returns a snapshot of the current in-progress candle.
func (ca *CandleAggregator) CurrentCandle(window time.Duration) (Candle, bool) {
	ca.mu.RLock()
	defer ca.mu.RUnlock()

	cur, exists := ca.candles[window]
	if !exists || cur.samples == 0 {
		return Candle{}, false
	}

	dir := DirectionDown
	if cur.close >= cur.open {
		dir = DirectionUp
	}

	return Candle{
		Open:      cur.open,
		Close:     cur.close,
		High:      cur.high,
		Low:       cur.low,
		Start:     cur.start,
		Direction: dir,
		Samples:   cur.samples,
		Partial:   cur.partial,
	}, true
}

// LastCompleted returns the most recently completed candle for a window.
func (ca *CandleAggregator) LastCompleted(window time.Duration) (Candle, bool) {
	ca.mu.RLock()
	defer ca.mu.RUnlock()

	hist := ca.history[window]
	if len(hist) == 0 {
		return Candle{}, false
	}
	return hist[len(hist)-1], true
}

// CompletedCandles returns all completed candles for a window (oldest first).
func (ca *CandleAggregator) CompletedCandles(window time.Duration) []Candle {
	ca.mu.RLock()
	defer ca.mu.RUnlock()

	hist := ca.history[window]
	out := make([]Candle, len(hist))
	copy(out, hist)
	return out
}
