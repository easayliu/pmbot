package engine

// ---------------------------------------------------------------------------
// JSON API response types for /api/paper/data and /api/paper/stream.
// These are parallel to the viewmodel types (PageData, MultiPageData, etc.)
// but fully JSON-serializable (no template.HTML / template.CSS).
// ---------------------------------------------------------------------------

// APIResponse is the top-level JSON envelope.
type APIResponse struct {
	Mode   string      `json:"mode"` // "single" or "multi"
	Single *SingleData `json:"single,omitempty"`
	Multi  *MultiData  `json:"multi,omitempty"`
}

// SingleData carries all data for a single paper-trading report (JSON).
type SingleData struct {
	Meta        APIMeta            `json:"meta"`
	Summary     APISummary         `json:"summary"`
	Metrics     APIMetrics         `json:"metrics"`
	HoldReasons []APIHoldReason    `json:"holdReasons"`
	TotalHolds  int                `json:"totalHolds"`
	Buckets     APIBuckets         `json:"buckets"`
	ProfitSim   APIProfitSim       `json:"profitSim"`
	Equity      APIEquity          `json:"equity"`
	Trades      []APITradeRow      `json:"trades"`
	Windows     APIWindows         `json:"windows"`
	History     []APIHistorySession `json:"history"`
	LivePaper   APILivePaper       `json:"livePaper"`
}

// MultiData carries all data for the multi-price report (JSON).
type MultiData struct {
	Meta      APIMeta              `json:"meta"`
	Summary   APIMultiSummary      `json:"summary"`
	LivePaper APILivePaper         `json:"livePaper"`
	Windows   APIWindows           `json:"windows"`
	Slots     []APISlotSummary     `json:"slots"`
	Details   []APISlotDetail      `json:"details"`
	History   []APIHistorySession  `json:"history"`
}

// APIMeta holds page-level metadata.
type APIMeta struct {
	Title     string `json:"title"`
	Label     string `json:"label,omitempty"`
	StartTime string `json:"startTime"`
	StartUnix int64  `json:"startUnix"`
	ServerTZ  string `json:"serverTZ"`
	EvalCount int    `json:"evalCount,omitempty"`
	SlotCount int    `json:"slotCount,omitempty"`
	DryRun    bool   `json:"dryRun"`
}

// APISummary holds summary stats for single-price reports.
type APISummary struct {
	TotalPnL    float64 `json:"totalPnL"`
	WinRate     float64 `json:"winRate"`
	TradeCount  int     `json:"tradeCount"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	Pending     int     `json:"pending"`
	AvgPnL      float64 `json:"avgPnL"`
	Resolved    int     `json:"resolved"`
	HasResolved bool    `json:"hasResolved"`
	Duration    string  `json:"duration"`
	Live        bool    `json:"live"`
}

// APIMultiSummary holds aggregate summary stats for multi-price reports.
type APIMultiSummary struct {
	TotalPnL    float64 `json:"totalPnL"`
	WinRate     float64 `json:"winRate"`
	TradeCount  int     `json:"tradeCount"`
	HasResolved bool    `json:"hasResolved"`
	Duration    string  `json:"duration"`
}

// APIMetrics holds risk/return metrics.
type APIMetrics struct {
	SharpeRatio    float64 `json:"sharpeRatio"`
	MaxDrawdown    float64 `json:"maxDrawdown"`
	MaxDrawdownPct float64 `json:"maxDrawdownPct"`
	ProfitFactor   float64 `json:"profitFactor"`
	Expectancy     float64 `json:"expectancy"`
	AvgWin         float64 `json:"avgWin"`
	AvgLoss        float64 `json:"avgLoss"`
	WinLossRatio   float64 `json:"winLossRatio"`
	MaxConsecWins  int     `json:"maxConsecWins"`
	MaxConsecLoss  int     `json:"maxConsecLoss"`
	RecoveryFactor float64 `json:"recoveryFactor"`
}

// APIHoldReason represents a hold reason row.
type APIHoldReason struct {
	Reason string  `json:"reason"`
	Count  int     `json:"count"`
	Pct    float64 `json:"pct"`
}

// APIBuckets groups all bucket analysis data.
type APIBuckets struct {
	Price     []APIBucketRow `json:"price"`
	Change    []APIBucketRow `json:"change"`
	Direction []APIBucketRow `json:"direction"`
	Timing    []APIBucketRow `json:"timing"`
}

// APIBucketRow represents a single bucket analysis row.
type APIBucketRow struct {
	Label    string  `json:"label"`
	Trades   int     `json:"trades"`
	Wins     int     `json:"wins"`
	Losses   int     `json:"losses"`
	WinRate  float64 `json:"winRate"`
	TotalPnL float64 `json:"totalPnL"`
}

// APIProfitSim holds profitability simulation data.
type APIProfitSim struct {
	ObservedWR     float64        `json:"observedWR"`
	BreakevenPrice float64        `json:"breakevenPrice"`
	Rows           []APIProfitRow `json:"rows"`
}

// APIProfitRow represents a profitability simulation row.
type APIProfitRow struct {
	BuyPrice     float64 `json:"buyPrice"`
	WinProfit    float64 `json:"winProfit"`
	TotalPnL     float64 `json:"totalPnL"`
	PerTrade     float64 `json:"perTrade"`
	NeedWR       float64 `json:"needWR"`
	IsProfitable bool    `json:"isProfitable"`
	IsBreakeven  bool    `json:"isBreakeven"`
	BarWidth     float64 `json:"barWidth"`
}

// APIEquity holds raw equity curve data for client-side SVG rendering.
type APIEquity struct {
	Points    []float64 `json:"points"`
	Peaks     []float64 `json:"peaks"`
	Drawdowns []float64 `json:"drawdowns"`
}

// APITradeRow represents a single trade.
type APITradeRow struct {
	Number    int     `json:"number"`
	Time      string  `json:"time"`
	Side      string  `json:"side"`
	BuyPrice  float64 `json:"buyPrice"`
	SellPrice float64 `json:"sellPrice,omitempty"`
	Change5m  float64 `json:"change5m"`
	Remaining int     `json:"remaining"`
	Resolved  bool    `json:"resolved"`
	Won       bool    `json:"won"`
	FinalDir  string  `json:"finalDir"`
	PnL       float64 `json:"pnl"`
	Live      bool    `json:"live"`
}

// APIWindows groups window result data.
type APIWindows struct {
	Results   []APIWindowRow `json:"results"`
	Count     int            `json:"count"`
	UpCount   int            `json:"upCount"`
	DownCount int            `json:"downCount"`
}

// APIWindowRow represents a single window result.
type APIWindowRow struct {
	EndTime      string          `json:"endTime"`
	Direction    string          `json:"direction"`
	Result       string          `json:"result"`
	MarketSignal string          `json:"marketSignal"`
	BTCOpen      float64         `json:"btcOpen"`
	BTCClose     float64         `json:"btcClose"`
	Change       float64         `json:"change"`
	Traded       bool            `json:"traded"`
	TradedSlots  []APITradedSlot `json:"tradedSlots,omitempty"`
}

// APITradedSlot represents a slot that traded in a window.
type APITradedSlot struct {
	Label string `json:"label"`
	Side  string `json:"side"`
	Won   bool   `json:"won"`
}

// APILivePaper holds live vs paper aggregate stats.
type APILivePaper struct {
	HasLive  bool   `json:"hasLive"`
	HasPaper bool   `json:"hasPaper"`
	Live     APIAgg `json:"live"`
	Paper    APIAgg `json:"paper"`
}

// APIAgg holds aggregate statistics for a group of slots.
type APIAgg struct {
	Trades      int     `json:"trades"`
	Wins        int     `json:"wins"`
	Losses      int     `json:"losses"`
	Resolved    int     `json:"resolved"`
	TotalPnL    float64 `json:"totalPnL"`
	WinRate     float64 `json:"winRate"`
	AvgPnL      float64 `json:"avgPnL"`
	HasResolved bool    `json:"hasResolved"`
}

// APISlotSummary represents a slot in the multi-price comparison table.
type APISlotSummary struct {
	Label       string        `json:"label"`
	Live        bool          `json:"live"`
	Trades      int           `json:"trades"`
	Wins        int           `json:"wins"`
	Losses      int           `json:"losses"`
	Resolved    int           `json:"resolved"`
	WinRate     float64       `json:"winRate"`
	AvgPnL      float64       `json:"avgPnL"`
	TotalPnL    float64       `json:"totalPnL"`
	IsBest      bool          `json:"isBest"`
	Metrics     APIMetrics    `json:"metrics"`
	HasResolved bool          `json:"hasResolved"`
	LastTrade   *APILastTrade `json:"lastTrade,omitempty"`
}

// APILastTrade holds data about the most recent trade.
type APILastTrade struct {
	Time     string  `json:"time"`
	Side     string  `json:"side"`
	BuyPrice float64 `json:"buyPrice"`
	Resolved bool    `json:"resolved"`
	Won      bool    `json:"won"`
	FinalDir string  `json:"finalDir"`
	PnL      float64 `json:"pnl"`
	Ago      string  `json:"ago"`
}

// APISlotDetail carries per-slot detail panel data.
type APISlotDetail struct {
	Label            string         `json:"label"`
	Live             bool           `json:"live"`
	Trades           int            `json:"trades"`
	Wins             int            `json:"wins"`
	Losses           int            `json:"losses"`
	Resolved         int            `json:"resolved"`
	WinRate          float64        `json:"winRate"`
	TotalPnL         float64        `json:"totalPnL"`
	AvgPnL           float64        `json:"avgPnL"`
	HasResolved      bool           `json:"hasResolved"`
	Metrics          APIMetrics     `json:"metrics"`
	LastTrade        *APILastTrade  `json:"lastTrade,omitempty"`
	Equity           APIEquity      `json:"equity"`
	DirectionBuckets []APIBucketRow `json:"directionBuckets"`
	TimingBuckets    []APIBucketRow `json:"timingBuckets"`
	TradeHistory     []APITradeRow  `json:"tradeHistory"`
}

// APIHistorySession represents a historical session.
type APIHistorySession struct {
	SessionID     string            `json:"sessionID"`
	StartedAt     string            `json:"startedAt"`
	Duration      string            `json:"duration"`
	Trades        int               `json:"trades"`
	Wins          int               `json:"wins"`
	Losses        int               `json:"losses"`
	Resolved      int               `json:"resolved"`
	WinRate       float64           `json:"winRate"`
	TotalPnL      float64           `json:"totalPnL"`
	SlotSummaries []APIHistorySlot  `json:"slotSummaries"`
	TradeDetails  []APIHistoryTrade `json:"tradeDetails"`
	FetchError    bool              `json:"fetchError"`
}

// APIHistorySlot represents a per-slot summary within a historical session.
type APIHistorySlot struct {
	Slot     string  `json:"slot"`
	Trades   int     `json:"trades"`
	Wins     int     `json:"wins"`
	Losses   int     `json:"losses"`
	Resolved int     `json:"resolved"`
	WinRate  float64 `json:"winRate"`
	PnL      float64 `json:"pnl"`
	Live     bool    `json:"live"`
}

// APIHistoryTrade represents a trade in historical session detail.
type APIHistoryTrade struct {
	Number    int     `json:"number"`
	Time      string  `json:"time"`
	SlotLabel string  `json:"slotLabel"`
	Side      string  `json:"side"`
	BuyPrice  float64 `json:"buyPrice"`
	Change5m  float64 `json:"change5m"`
	Remaining int     `json:"remaining"`
	Resolved  bool    `json:"resolved"`
	Won       bool    `json:"won"`
	FinalDir  string  `json:"finalDir"`
	PnL       float64 `json:"pnl"`
	Live      bool    `json:"live"`
}
