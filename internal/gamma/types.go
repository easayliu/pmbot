package gamma

import "time"

// Market represents a prediction market on Polymarket.
type Market struct {
	ID                    string  `json:"id"`
	Question              string  `json:"question"`
	ConditionID           string  `json:"conditionId"`
	Slug                  string  `json:"slug"`
	ResolutionSource      string  `json:"resolutionSource"`
	EndDate               string  `json:"endDate"`
	Liquidity             string  `json:"liquidity"`
	StartDate             string  `json:"startDate"`
	Image                 string  `json:"image"`
	Icon                  string  `json:"icon"`
	Description           string  `json:"description"`
	Outcomes              string  `json:"outcomes"`
	OutcomePrices         string  `json:"outcomePrices"`
	Volume                string  `json:"volume"`
	Active                bool    `json:"active"`
	Closed                bool    `json:"closed"`
	MarketMakerAddress    string  `json:"marketMakerAddress"`
	CreatedAt             string  `json:"createdAt"`
	UpdatedAt             string  `json:"updatedAt"`
	New                   bool    `json:"new"`
	Featured              bool    `json:"featured"`
	SubmittedBy           string  `json:"submitted_by"`
	Archived              bool    `json:"archived"`
	ResolvedBy            string  `json:"resolvedBy"`
	Restricted            bool    `json:"restricted"`
	GroupItemTitle        string  `json:"groupItemTitle"`
	GroupItemThreshold    string  `json:"groupItemThreshold"`
	QuestionID            string  `json:"questionID"`
	EnableOrderBook       bool    `json:"enableOrderBook"`
	OrderPriceMinTickSize float64 `json:"orderPriceMinTickSize"`
	OrderMinSize          float64 `json:"orderMinSize"`
	VolumeNum             float64 `json:"volumeNum"`
	LiquidityNum          float64 `json:"liquidityNum"`
	EndDateIso            string  `json:"endDateIso"`
	StartDateIso          string  `json:"startDateIso"`
	HasReviewedDates      bool    `json:"hasReviewedDates"`
	Volume24hr            float64 `json:"volume24hr"`
	Volume1wk             float64 `json:"volume1wk"`
	Volume1mo             float64 `json:"volume1mo"`
	Volume1yr             float64 `json:"volume1yr"`
	ClobTokenIDs          string  `json:"clobTokenIds"`
	UmaBond               string  `json:"umaBond"`
	UmaReward             string  `json:"umaReward"`
	Volume24hrClob        float64 `json:"volume24hrClob"`
	Volume1wkClob         float64 `json:"volume1wkClob"`
	Volume1moClob         float64 `json:"volume1moClob"`
	Volume1yrClob         float64 `json:"volume1yrClob"`
	VolumeClob            float64 `json:"volumeClob"`
	LiquidityClob         float64 `json:"liquidityClob"`
	AcceptingOrders       bool    `json:"acceptingOrders"`
	NegRisk               bool    `json:"negRisk"`
	NegRiskMarketID       string  `json:"negRiskMarketID"`
	NegRiskRequestID      string  `json:"negRiskRequestID"`
	Spread                float64 `json:"spread"`
	LastTradePrice        float64 `json:"lastTradePrice"`
	BestAsk               float64 `json:"bestAsk"`
	BestBid               float64 `json:"bestBid"`
	OneDayPriceChange     float64 `json:"oneDayPriceChange"`
	Events                []Event `json:"events,omitempty"`
	Tags                  []Tag   `json:"tags,omitempty"`
}

// Event represents a collection of related markets on Polymarket.
type Event struct {
	ID                  string   `json:"id"`
	Ticker              string   `json:"ticker"`
	Slug                string   `json:"slug"`
	Title               string   `json:"title"`
	Description         string   `json:"description"`
	ResolutionSource    string   `json:"resolutionSource"`
	StartDate           string   `json:"startDate"`
	CreationDate        string   `json:"creationDate"`
	EndDate             string   `json:"endDate"`
	Image               string   `json:"image"`
	Icon                string   `json:"icon"`
	Active              bool     `json:"active"`
	Closed              bool     `json:"closed"`
	Archived            bool     `json:"archived"`
	New                 bool     `json:"new"`
	Featured            bool     `json:"featured"`
	Restricted          bool     `json:"restricted"`
	Liquidity           float64  `json:"liquidity"`
	Volume              float64  `json:"volume"`
	OpenInterest        float64  `json:"openInterest"`
	CreatedAt           string   `json:"createdAt"`
	UpdatedAt           string   `json:"updatedAt"`
	Competitive         float64  `json:"competitive"`
	Volume24hr          float64  `json:"volume24hr"`
	Volume1wk           float64  `json:"volume1wk"`
	Volume1mo           float64  `json:"volume1mo"`
	Volume1yr           float64  `json:"volume1yr"`
	EnableOrderBook     bool     `json:"enableOrderBook"`
	LiquidityClob       float64  `json:"liquidityClob"`
	NegRisk             bool     `json:"negRisk"`
	NegRiskMarketID     string   `json:"negRiskMarketID"`
	CommentCount        int      `json:"commentCount"`
	Markets             []Market `json:"markets,omitempty"`
	Series              []Series `json:"series,omitempty"`
	Cyom                bool     `json:"cyom"`
	ShowAllOutcomes     bool     `json:"showAllOutcomes"`
	ShowMarketImages    bool     `json:"showMarketImages"`
	EnableNegRisk       bool     `json:"enableNegRisk"`
	AutomaticallyActive bool     `json:"automaticallyActive"`
	SeriesSlug          string   `json:"seriesSlug"`
	GmpChartMode        string   `json:"gmpChartMode"`
}

// Series represents a recurring series of events.
type Series struct {
	ID                  string  `json:"id"`
	Ticker              string  `json:"ticker"`
	Slug                string  `json:"slug"`
	Title               string  `json:"title"`
	SeriesType          string  `json:"seriesType"`
	Recurrence          string  `json:"recurrence"`
	Image               string  `json:"image"`
	Icon                string  `json:"icon"`
	Active              bool    `json:"active"`
	Closed              bool    `json:"closed"`
	Archived            bool    `json:"archived"`
	Featured            bool    `json:"featured"`
	Restricted          bool    `json:"restricted"`
	CreatedAt           string  `json:"createdAt"`
	UpdatedAt           string  `json:"updatedAt"`
	Volume24hr          float64 `json:"volume24hr"`
	Volume              float64 `json:"volume"`
	Liquidity           float64 `json:"liquidity"`
	CommentCount        int     `json:"commentCount"`
	RequiresTranslation bool    `json:"requiresTranslation"`
}

// Tag represents a category tag for markets/events.
type Tag struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// ListMarketsParams contains query parameters for the list markets endpoint.
type ListMarketsParams struct {
	Limit           int
	Offset          int
	Order           string
	Ascending       *bool
	ID              []int
	Slug            []string
	ClobTokenIDs    []string
	ConditionIDs    []string
	LiquidityNumMin *float64
	LiquidityNumMax *float64
	VolumeNumMin    *float64
	VolumeNumMax    *float64
	StartDateMin    *time.Time
	StartDateMax    *time.Time
	EndDateMin      *time.Time
	EndDateMax      *time.Time
	TagID           *int
	RelatedTags     *bool
	Closed          *bool
	Active          *bool
}

// ListEventsParams contains query parameters for the list events endpoint.
type ListEventsParams struct {
	Limit     int
	Offset    int
	Order     string
	Ascending *bool
	Closed    *bool
	Active    *bool
	TagID     *int
	Slug      string
}
