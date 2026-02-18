package clob

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Sentinel errors for precondition checks.
var (
	// ErrNoPrivateKey is returned when an operation requires a private key but none is configured.
	ErrNoPrivateKey = errors.New("private key is required")
	// ErrNoCredentials is returned when an operation requires API credentials but none are available.
	ErrNoCredentials = errors.New("api credentials required: call CreateAPIKey or DeriveAPIKey first")
	// ErrNoSessionCredentials is returned when an operation requires session cookies but none are configured.
	ErrNoSessionCredentials = errors.New("session credentials required: set POLYMARKET_COOKIES env var")
)

const (
	// DefaultBaseURL is the default CLOB API base URL.
	DefaultBaseURL = "https://clob.polymarket.com"
	// DefaultTimeout is the default HTTP client timeout.
	DefaultTimeout = 30 * time.Second
	// DefaultChainID is the Polygon mainnet chain ID.
	DefaultChainID = 137
)

// Client is an HTTP client for the Polymarket CLOB API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	chainID    int
	privateKey *ecdsa.PrivateKey
	address    common.Address
	funder     common.Address // For proxy wallets; equals address for EOA.
	polyAddr   common.Address // Polymarket wallet address (configured or derived).
	sigType     SignatureType
	credentials *APICredentials
	polygonRPC  string // Optional Polygon JSON-RPC for on-chain nonce queries.
	initErr     error  // Deferred initialization error from Option funcs.
}

// Option configures the Client.
type Option func(*Client)

// WithBaseURL overrides the default base URL.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = baseURL
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithChainID sets the blockchain chain ID.
func WithChainID(chainID int) Option {
	return func(c *Client) {
		c.chainID = chainID
	}
}

// WithPrivateKey sets the private key for authentication and order signing.
func WithPrivateKey(hexKey string) Option {
	return func(c *Client) {
		key, err := crypto.HexToECDSA(strings.TrimPrefix(hexKey, "0x"))
		if err != nil {
			c.initErr = fmt.Errorf("invalid private key: %w", err)
			return
		}
		c.privateKey = key
		c.address = crypto.PubkeyToAddress(key.PublicKey)
		c.funder = c.address
	}
}

// WithCredentials sets pre-existing API credentials for L2 authentication.
func WithCredentials(creds APICredentials) Option {
	return func(c *Client) {
		c.credentials = &creds
	}
}

// WithSignatureType sets the wallet signature type.
func WithSignatureType(sigType SignatureType) Option {
	return func(c *Client) {
		c.sigType = sigType
	}
}

// WithPolygonRPC sets the Polygon JSON-RPC URL for on-chain queries (e.g., nonce reads).
func WithPolygonRPC(rpcURL string) Option {
	return func(c *Client) {
		c.polygonRPC = rpcURL
	}
}

// WithFunder sets a different funder address (for proxy wallets).
func WithFunder(addr string) Option {
	return func(c *Client) {
		c.funder = common.HexToAddress(addr)
	}
}

// WithPolymarketAddress sets the Polymarket wallet address directly,
// bypassing the on-chain derivation.
func WithPolymarketAddress(addr string) Option {
	return func(c *Client) {
		c.polyAddr = common.HexToAddress(addr)
	}
}

// NewClient creates a new CLOB API client.
func NewClient(opts ...Option) (*Client, error) {
	c := &Client{
		baseURL: DefaultBaseURL,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		chainID: DefaultChainID,
		sigType: SignatureTypeEOA,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.initErr != nil {
		return nil, c.initErr
	}
	// Auto-derive funder (Polymarket proxy wallet) from EOA if not explicitly configured.
	// Polymarket orders require maker = proxy wallet address, not EOA.
	if c.privateKey != nil && c.funder == c.address {
		c.funder = c.PolymarketAddress()
		// When using a derived proxy wallet as funder, the signature type must
		// match so the exchange verifies the order signature correctly.
		// EOA (0) means verify against maker directly — wrong when maker is a proxy.
		if c.sigType == SignatureTypeEOA {
			c.sigType = c.detectSignatureType()
		}
	}
	return c, nil
}

// Address returns the signer wallet address.
func (c *Client) Address() common.Address {
	return c.address
}

// SigType returns the signature type used for order signing.
func (c *Client) SigType() SignatureType {
	return c.sigType
}

// Funder returns the funder (proxy wallet) address.
// For EOA wallets this equals Address(); for proxy wallets it differs.
func (c *Client) Funder() common.Address {
	return c.funder
}

// PolymarketAddress returns the Polymarket wallet address.
// If explicitly configured via WithPolymarketAddress, returns that;
// otherwise falls back to on-chain derivation from the EOA.
func (c *Client) PolymarketAddress() common.Address {
	if (c.polyAddr != common.Address{}) {
		return c.polyAddr
	}
	return DerivePolymarketAddress(c.address, c.sigType)
}

// detectSignatureType auto-detects the correct signature type by comparing the
// configured Polymarket wallet address against both derivation methods.
// Falls back to SignatureTypePolyProxy if neither matches (most common on Polymarket).
func (c *Client) detectSignatureType() SignatureType {
	target := c.funder
	if derived := DeriveProxyAddress(c.address); derived == target {
		slog.Info("auto-detected signature type", "type", "POLY_PROXY", "value", int(SignatureTypePolyProxy))
		return SignatureTypePolyProxy
	}
	if derived := DeriveSafeAddress(c.address); derived == target {
		slog.Info("auto-detected signature type", "type", "GNOSIS_SAFE", "value", int(SignatureTypeGnosisSafe))
		return SignatureTypeGnosisSafe
	}
	// Neither derivation matched; default to Poly Proxy.
	slog.Warn("could not auto-detect signature type, defaulting to POLY_PROXY",
		"funder", target.Hex(), "eoa", c.address.Hex())
	return SignatureTypePolyProxy
}

// Credentials returns the current API credentials, or nil if not authenticated.
func (c *Client) Credentials() *APICredentials {
	return c.credentials
}

// get performs an unauthenticated GET request.
func (c *Client) get(ctx context.Context, path string, params url.Values, dest any) error {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if params != nil {
		u.RawQuery = params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	slog.Debug("http request", "method", "GET", "url", u.String())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// post performs an unauthenticated POST request.
func (c *Client) post(ctx context.Context, path string, body any, dest any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	slog.Debug("http request", "method", "POST", "url", u)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// doRequest performs an authenticated HTTP request with custom headers.
func (c *Client) doRequest(ctx context.Context, method, path string, headers map[string]string, body any, dest any) error {
	var reqBody io.Reader
	var bodyStr string
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyStr = string(data)
		reqBody = bytes.NewReader(data)
	}

	u := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	slog.Debug("http request", "method", method, "url", u, "body_bytes", len(bodyStr))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		// Parse structured error response {"error": "..."}.
		var errResp struct {
			Error string `json:"error"`
		}
		msg := string(respBody)
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			msg = errResp.Error
		}
		return &APIError{StatusCode: resp.StatusCode, Message: msg}
	}

	if dest != nil {
		if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// APIError represents a structured error response from the CLOB API.
type APIError struct {
	StatusCode int
	Message    string
}

// Error returns a human-readable representation of the API error.
func (e *APIError) Error() string {
	return fmt.Sprintf("api error %d: %s", e.StatusCode, e.Message)
}

// IsRetryable reports whether the error is transient and the request should be retried.
func (e *APIError) IsRetryable() bool {
	switch e.StatusCode {
	case 425, 429, 500:
		return true
	default:
		return false
	}
}

// IsRetryableError checks whether any error in the chain is a retryable API error.
func IsRetryableError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsRetryable()
	}
	// Network-level transient errors.
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "TLS handshake timeout")
}

// doL2Request performs an L2-authenticated request (HMAC-SHA256).
func (c *Client) doL2Request(ctx context.Context, method, path string, body any, dest any) error {
	var bodyStr string
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyStr = string(data)
	}

	headers, err := c.buildL2Headers(method, path, bodyStr)
	if err != nil {
		return fmt.Errorf("build l2 headers: %w", err)
	}
	return c.doRequest(ctx, method, path, headers, body, dest)
}
