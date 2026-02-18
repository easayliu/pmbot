package clob

import "encoding/json"

// OrderBook represents an order book snapshot for a single token.
type OrderBook struct {
	Market       string       `json:"market"`
	AssetID      string       `json:"asset_id"`
	Timestamp    string       `json:"timestamp"`
	Hash         string       `json:"hash"`
	Bids         []OrderLevel `json:"bids"`
	Asks         []OrderLevel `json:"asks"`
	MinOrderSize string       `json:"min_order_size"`
	TickSize     string       `json:"tick_size"`
	NegRisk      bool         `json:"neg_risk"`
}

// OrderLevel represents a single price level in the order book.
type OrderLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// BookRequest is used for batch order book / pricing requests.
type BookRequest struct {
	TokenID string `json:"token_id"`
	Side    string `json:"side,omitempty"`
}

// PriceResponse is the response from the /price endpoint.
type PriceResponse struct {
	Price string `json:"price"`
}

// MidpointResponse is the response from the /midpoint endpoint.
type MidpointResponse struct {
	Mid string `json:"mid"`
}

// SpreadResponse is the response from the /spread endpoint.
type SpreadResponse struct {
	Spread string `json:"spread"`
}

// LastTradePriceResponse is the response from the /last-trade-price endpoint.
type LastTradePriceResponse struct {
	Price string `json:"price"`
	Side  string `json:"side"`
}

// HistoryPoint represents a single point in price history.
type HistoryPoint struct {
	Timestamp int64   `json:"t"`
	Price     float64 `json:"p"`
}

// PriceHistoryResponse is the response from the /prices-history endpoint.
type PriceHistoryResponse struct {
	History []HistoryPoint `json:"history"`
}

// APICredentials holds the CLOB API authentication credentials (L2).
type APICredentials struct {
	Key        string `json:"apiKey"`
	Secret     string `json:"secret"`
	Passphrase string `json:"passphrase"`
}

// SignatureType represents the type of wallet signature.
type SignatureType int

const (
	// SignatureTypeEOA is for standard Ethereum wallets (MetaMask, hardware wallets).
	SignatureTypeEOA SignatureType = 0
	// SignatureTypePolyProxy is for Magic Link / Email wallets.
	SignatureTypePolyProxy SignatureType = 1
	// SignatureTypeGnosisSafe is for multisig proxy wallets.
	SignatureTypeGnosisSafe SignatureType = 2
)

// OrderType represents the order execution type.
type OrderType string

const (
	OrderTypeGTC OrderType = "GTC" // Good Till Cancelled
	OrderTypeFOK OrderType = "FOK" // Fill or Kill
	OrderTypeGTD OrderType = "GTD" // Good Till Date
	OrderTypeFAK OrderType = "FAK" // Fill and Kill
)

// Order represents a CLOB order to be signed via EIP-712.
type Order struct {
	// Salt must serialize as a JSON number (not quoted string) per the CLOB API.
	Salt          json.Number `json:"salt"`
	Maker         string      `json:"maker"`
	Signer        string      `json:"signer"`
	Taker         string      `json:"taker"`
	TokenID       string      `json:"tokenId"`
	MakerAmount   string      `json:"makerAmount"`
	TakerAmount   string      `json:"takerAmount"`
	Expiration    string      `json:"expiration"`
	Nonce         string      `json:"nonce"`
	FeeRateBps    string      `json:"feeRateBps"`
	Side          string      `json:"side"`
	SignatureType int         `json:"signatureType"`
	Signature     string      `json:"signature"`
}

// PlaceOrderRequest is the request body for POST /order.
type PlaceOrderRequest struct {
	Order     Order  `json:"order"`
	Owner     string `json:"owner"`
	OrderType string `json:"orderType"`
	PostOnly  bool   `json:"postOnly"`
	DeferExec bool   `json:"deferExec"`
}

// OrderResponse is the response from POST /order.
type OrderResponse struct {
	Success            bool     `json:"success"`
	ErrorMsg           string   `json:"errorMsg"`
	OrderID            string   `json:"orderID"`
	TransactionsHashes []string `json:"transactionsHashes"`
	TradeIDs           []string `json:"tradeIDs"`
	Status             string   `json:"status"`
	TakingAmount       string   `json:"takingAmount"`
	MakingAmount       string   `json:"makingAmount"`
}

// CancelResponse is the response from DELETE /order.
type CancelResponse struct {
	Canceled    []string          `json:"canceled"`
	NotCanceled map[string]string `json:"not_canceled"`
}

// OpenOrder represents an active order in the system.
type OpenOrder struct {
	ID               string `json:"id"`
	Market           string `json:"market"`
	AssetID          string `json:"asset_id"`
	Side             string `json:"side"`
	OriginalSize     string `json:"original_size"`
	SizeMatched      string `json:"size_matched"`
	Price            string `json:"price"`
	Status           string `json:"status"`
	OrderType        string `json:"type"`
	CreatedAt        string `json:"created_at"`
	ExpirationTs     string `json:"expiration"`
	AssociateTradeID string `json:"associate_trades"`
	Owner            string `json:"owner"`
	MakerAddress     string `json:"maker_address"`
}

// OpenOrdersResponse is the paginated response for GET /data/orders.
type OpenOrdersResponse struct {
	Data       []OpenOrder `json:"data"`
	NextCursor string      `json:"next_cursor"`
}

// Trade represents an executed trade.
type Trade struct {
	ID              string `json:"id"`
	Market          string `json:"market"`
	AssetID         string `json:"asset_id"`
	Side            string `json:"side"`
	Size            string `json:"size"`
	Price           string `json:"price"`
	Status          string `json:"status"`
	MakerAddress    string `json:"maker_address"`
	MatchTime       string `json:"match_time"`
	TradeOwner      string `json:"owner"`
	TransactionHash string `json:"transaction_hash"`
	Fee             string `json:"fee"`
	OrderID         string `json:"order_id"`
}

// TradesResponse is the paginated response for GET /data/trades.
type TradesResponse struct {
	Data       []Trade `json:"data"`
	NextCursor string  `json:"next_cursor"`
}

// OrderArgs contains user-facing parameters for creating a limit order.
type OrderArgs struct {
	TokenID    string
	Price      float64
	Size       float64
	Side       string // "BUY" or "SELL"
	OrderType  OrderType
	Expiration int64 // Unix timestamp, 0 = no expiration
	FeeRateBps int   // Fee rate in basis points (fetched from /fee-rate endpoint)
}

// MarketOrderArgs contains parameters for creating a market order (FOK/FAK).
// For BUY: Amount is dollars to spend, Price is max acceptable price.
// For SELL: Amount is shares to sell, Price is min acceptable price.
type MarketOrderArgs struct {
	TokenID    string
	Amount     float64
	Price      float64 // Worst acceptable price (slippage protection)
	Side       string  // "BUY" or "SELL"
	Expiration int64   // Unix timestamp, 0 = no expiration
	FeeRateBps int
}

// PlaceOrdersItem is a single order in a batch POST /orders request.
type PlaceOrdersItem struct {
	Order     Order  `json:"order"`
	OrderType string `json:"orderType"`
	PostOnly  bool   `json:"postOnly,omitempty"`
}

// HeartbeatResponse is the response from POST /heartbeat.
type HeartbeatResponse struct {
	HeartbeatID string `json:"heartbeat_id"`
}
