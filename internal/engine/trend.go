package engine

import (
	"math"
	"time"

	"github.com/easay/pmbot/internal/strategy"
)

const (
	// trendBufSize holds 5 minutes + 1 sample at 1 sample/sec,
	// so lookback(300) gives exactly 5 minutes ago.
	trendBufSize = 301

	// ewmaLambda is the EWMA decay factor for volatility estimation.
	// Half-life ≈ 60 samples (1 minute): lambda = exp(-ln2/60) ≈ 0.9885.
	// Standard in quantitative finance (cf. RiskMetrics EWMA model).
	ewmaLambda = 0.9885
)

// TrendTracker computes price trend metrics incrementally using a ring buffer
// and EWMA (Exponentially Weighted Moving Average) volatility.
//
// Sampling is wall-clock aligned: ticks are deduplicated by truncating the
// server timestamp to the nearest second boundary. This ensures that two bot
// instances started at different times select the same set of 1-second samples
// (given the same price feed), producing identical trend metrics regardless of
// startup timing.
//
// Industry-standard approach:
//   - Ring buffer: O(1) insert, O(1) lookback by index
//   - EWMA volatility: O(1) per update, more responsive to recent changes
//   - Fixed memory: exactly trendBufSize samples, no allocations after init
type TrendTracker struct {
	buf   [trendBufSize]trendSample
	head  int       // next write index
	count int       // total samples stored (capped at trendBufSize)
	lastS time.Time // wall-clock aligned second (tick.Time truncated to second)

	// EWMA volatility of 1-second price deltas.
	ewmaVar float64
	hasVar  bool
}

type trendSample struct {
	price float64
	time  time.Time
}

// Add records a price sample, deduplicated by wall-clock second.
// The tick timestamp is truncated to the nearest second boundary so that
// different bot instances (started at different times) always select the
// same sample per second from the same price feed, producing deterministic
// trend metrics regardless of startup timing.
// Updates the EWMA volatility incrementally on each new sample.
func (t *TrendTracker) Add(price float64, now time.Time) {
	sec := now.Truncate(time.Second)
	if !t.lastS.IsZero() && sec.Equal(t.lastS) {
		return // already sampled this wall-clock second
	}

	// Update EWMA volatility from the delta to the previous sample.
	if t.count > 0 {
		prev := t.newest()
		delta := price - prev.price
		if t.hasVar {
			t.ewmaVar = ewmaLambda*t.ewmaVar + (1-ewmaLambda)*delta*delta
		} else {
			t.ewmaVar = delta * delta
			t.hasVar = true
		}
	}

	t.buf[t.head] = trendSample{price: price, time: now}
	t.head = (t.head + 1) % trendBufSize
	if t.count < trendBufSize {
		t.count++
	}
	t.lastS = sec
}

// Compute returns the current trend metrics in O(1).
func (t *TrendTracker) Compute() strategy.PriceTrend {
	if t.count < 2 {
		return strategy.PriceTrend{Samples: t.count}
	}

	latest := t.newest()

	var change1m, change5m, speed1m, speed5m float64

	// Price ~1 minute ago (60 samples back).
	if p, ok := t.lookback(60); ok {
		change1m = latest.price - p
		speed1m = change1m // per minute
	} else {
		// Not enough for 1 min — extrapolate from available range.
		oldest := t.oldest()
		elapsed := latest.time.Sub(oldest.time).Minutes()
		if elapsed > 0 {
			change1m = latest.price - oldest.price
			speed1m = change1m / elapsed
		}
	}

	// Price ~5 minutes ago (300 samples back).
	if p, ok := t.lookback(300); ok {
		change5m = latest.price - p
		speed5m = change5m / 5.0
	} else {
		oldest := t.oldest()
		elapsed := latest.time.Sub(oldest.time).Minutes()
		if elapsed > 0 {
			change5m = latest.price - oldest.price
			speed5m = change5m / elapsed
		}
	}

	return strategy.PriceTrend{
		Change1m:   change1m,
		Change5m:   change5m,
		Speed1m:    speed1m,
		Speed5m:    speed5m,
		Volatility: math.Sqrt(t.ewmaVar),
		Samples:    t.count,
	}
}

// newest returns the most recently added sample.
func (t *TrendTracker) newest() trendSample {
	idx := (t.head - 1 + trendBufSize) % trendBufSize
	return t.buf[idx]
}

// oldest returns the oldest sample in the buffer.
func (t *TrendTracker) oldest() trendSample {
	if t.count < trendBufSize {
		return t.buf[0]
	}
	return t.buf[t.head] // head points to the oldest when buffer is full
}

// lookback returns the price n samples ago from the newest.
// lookback(0) = newest, lookback(60) ≈ 1 minute ago.
// Returns false if not enough history.
func (t *TrendTracker) lookback(n int) (float64, bool) {
	if n >= t.count {
		return 0, false
	}
	idx := (t.head - 1 - n + trendBufSize*2) % trendBufSize
	return t.buf[idx].price, true
}
