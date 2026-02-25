package feed

import "time"

// PriceTick represents a price update from an external data source.
type PriceTick struct {
	Symbol string
	Price  float64
	Time   time.Time
}
