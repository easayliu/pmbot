package clob

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	// RelayerBaseURL is the Polymarket relayer API base URL.
	RelayerBaseURL = "https://relayer-v2.polymarket.com"

	// Contract addresses on Polygon mainnet.
	CTFAddress              = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"
	USDCeAddress            = "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"
	NegRiskAdapterAddress   = "0xd91E80cF2E7be2e162c6513ceD06f1dD0dA35296"
	RelayHubAddress         = "0xD216153c06E857cD7f72665E0aF1d7D82172F494"

	// DefaultPolygonRPC is the public Polygon mainnet RPC endpoint used as fallback
	// when no explicit RPC URL is configured (for relay hub nonce queries only).
	DefaultPolygonRPC = "https://tenderly.rpc.polygon.community"

	// Gas constants for relay transactions.
	// Website uses ~200k for 1 redeem call; scale linearly for batch.
	gasPerRedeem = 120000 // gas per inner proxy call (redeemPositions or setApprovalForAll)
	gasRelayBase = 85000  // base GSN relay + proxy dispatch overhead
)

// ABI function selectors.
var (
	// keccak256("proxy((uint8,address,uint256,bytes)[])")[:4]
	proxySelector = mustHexDecode("34ee9791")
	// keccak256("redeemPositions(address,bytes32,bytes32,uint256[])")[:4] — CTF
	redeemSelector = mustHexDecode("01b7037c")
	// keccak256("redeemPositions(bytes32,uint256[])")[:4] — NegRiskAdapter
	negRiskRedeemSelector = mustHexDecode("dbeccb23")
	// keccak256("setApprovalForAll(address,bool)")[:4] — ERC1155
	setApprovalForAllSelector = mustHexDecode("a22cb465")
)

// RedeemRequest describes a single condition to redeem, with neg-risk awareness.
type RedeemRequest struct {
	ConditionID string
	NegRisk     bool
	// Amounts per outcome in on-chain units (1e6). Only used for neg-risk.
	// [0] = YES (outcomeIndex 0), [1] = NO (outcomeIndex 1).
	Amounts [2]*big.Int
}

// RelayPayloadResponse is the response from GET /relay-payload.
type RelayPayloadResponse struct {
	Address string `json:"address"` // relay server address
	Nonce   string `json:"nonce"`   // current nonce for the proxy wallet
}

// RelaySubmitRequest is the request body for POST /submit.
type RelaySubmitRequest struct {
	From            string                `json:"from"`
	To              string                `json:"to"`
	ProxyWallet     string                `json:"proxyWallet"`
	Data            string                `json:"data"`
	Nonce           string                `json:"nonce"`
	Signature       string                `json:"signature"`
	SignatureParams RelaySignatureParams  `json:"signatureParams"`
	Type            string                `json:"type"`
	Metadata        string                `json:"metadata"`
}

// RelaySignatureParams contains GSN relay parameters.
type RelaySignatureParams struct {
	GasPrice   string `json:"gasPrice"`
	GasLimit   string `json:"gasLimit"`
	RelayerFee string `json:"relayerFee"`
	RelayHub   string `json:"relayHub"`
	Relay      string `json:"relay"`
}

// RelaySubmitResponse is the response from POST /submit.
type RelaySubmitResponse struct {
	TransactionID   string `json:"transactionID"`
	State           string `json:"state"`
	TransactionHash string `json:"transactionHash"`
}

// GetRelayPayload fetches the relay server address from the API, then reads
// the authoritative nonce directly from the relay hub contract on-chain.
// The relay-payload API returns a per-relay-server nonce which diverges from
// the relay hub's global EOA nonce; querying on-chain ensures correctness.
func (c *Client) GetRelayPayload(ctx context.Context) (*RelayPayloadResponse, error) {
	proxyAddr := c.Funder().Hex()
	q := url.Values{
		"address": {proxyAddr},
		"type":    {"PROXY"},
	}
	fullURL := RelayerBaseURL + "/relay-payload?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay-payload status %d: %s", resp.StatusCode, string(body))
	}

	var result RelayPayloadResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode relay-payload: %w (body: %s)", err, string(body))
	}

	// Replace the API nonce with the authoritative on-chain nonce from the relay hub.
	// The API returns a per-relay-server nonce that may differ from the global hub nonce.
	rpcURL := c.polygonRPC
	if rpcURL == "" {
		rpcURL = DefaultPolygonRPC
	}
	onChainNonce, err := c.readRelayHubNonceVia(ctx, rpcURL)
	if err != nil {
		slog.Warn("relay: on-chain nonce read failed, falling back to API nonce",
			"rpc", rpcURL, "api_nonce", result.Nonce, "err", err)
	} else {
		slog.Info("relay: nonce resolved",
			"api_nonce", result.Nonce,
			"onchain_nonce", onChainNonce)
		result.Nonce = onChainNonce
	}

	return &result, nil
}

// RedeemPositions redeems outcome tokens for a given condition via the relayer.
// For regular markets: calls CTF.redeemPositions directly.
// For neg-risk markets: calls NegRiskAdapter.redeemPositions with token amounts.
func (c *Client) RedeemPositions(ctx context.Context, req RedeemRequest) (*RelaySubmitResponse, error) {
	if c.privateKey == nil {
		return nil, ErrNoPrivateKey
	}

	// Step 1: Get nonce and relay address.
	rp, err := c.GetRelayPayload(ctx)
	if err != nil {
		return nil, fmt.Errorf("get relay payload: %w", err)
	}

	// Step 2: Build ABI-encoded calldata via proxy multi-call.
	calls := BuildRedeemCalls(req)
	outerData := EncodeProxyMultiCall(calls)

	// Step 3: Calculate gas limit.
	gasLimit := calculateGasLimit(len(calls))

	// Step 4: Sign the GSN meta-transaction.
	from := c.address
	to := proxyFactory // ProxyWalletFactory from wallet.go
	sig, err := c.signRelayTx(from, to, outerData, "0", "0", gasLimit, rp.Nonce, RelayHubAddress, rp.Address)
	if err != nil {
		return nil, fmt.Errorf("sign relay tx: %w", err)
	}

	// Step 5: Build and submit the relay request.
	submitReq := RelaySubmitRequest{
		From:        from.Hex(),
		To:          to.Hex(),
		ProxyWallet: c.Funder().Hex(),
		Data:        "0x" + hex.EncodeToString(outerData),
		Nonce:       rp.Nonce,
		Signature:   sig,
		SignatureParams: RelaySignatureParams{
			GasPrice:   "0",
			GasLimit:   gasLimit,
			RelayerFee: "0",
			RelayHub:   RelayHubAddress,
			Relay:      rp.Address,
		},
		Type:     "PROXY",
		Metadata: "",
	}

	slog.Debug("submitting redeem relay tx",
		"condition_id", truncCondID(req.ConditionID),
		"neg_risk", req.NegRisk,
		"nonce", rp.Nonce,
		"proxy", c.Funder().Hex())

	data, err := json.Marshal(submitReq)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return c.doRelayPost(ctx, data)
}

// RedeemOnChain sends a direct on-chain transaction to the proxy wallet to redeem positions.
// This bypasses the relayer — the EOA pays gas in MATIC directly.
// rpcURL should be a Polygon mainnet JSON-RPC endpoint.
func (c *Client) RedeemOnChain(ctx context.Context, req RedeemRequest, rpcURL string) (string, error) {
	if c.privateKey == nil {
		return "", ErrNoPrivateKey
	}

	ethClient, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return "", fmt.Errorf("connect to polygon rpc: %w", err)
	}
	defer ethClient.Close()

	// Build calldata: proxy([(CALL, target, 0, redeemCalldata)])
	calls := BuildRedeemCalls(req)
	outerData := EncodeProxyMultiCall(calls)

	// Target: the proxy wallet (not the factory).
	proxyWallet := c.Funder()

	nonce, err := ethClient.PendingNonceAt(ctx, c.address)
	if err != nil {
		return "", fmt.Errorf("get nonce: %w", err)
	}

	gasPrice, err := ethClient.SuggestGasPrice(ctx)
	if err != nil {
		return "", fmt.Errorf("suggest gas price: %w", err)
	}

	// Cap gas price at 100 gwei to avoid overpaying on unreliable public RPCs.
	maxGasPrice := new(big.Int).Mul(big.NewInt(100), big.NewInt(1e9))
	if gasPrice.Cmp(maxGasPrice) > 0 {
		slog.Debug("capping gas price", "suggested", gasPrice.String(), "max", maxGasPrice.String())
		gasPrice = maxGasPrice
	}

	chainID := big.NewInt(int64(c.chainID))

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &proxyWallet,
		Value:    big.NewInt(0),
		Gas:      300000,
		GasPrice: gasPrice,
		Data:     outerData,
	})

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), c.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign transaction: %w", err)
	}

	if err := ethClient.SendTransaction(ctx, signedTx); err != nil {
		return "", fmt.Errorf("send transaction: %w", err)
	}

	txHash := signedTx.Hash().Hex()
	slog.Debug("on-chain redeem submitted",
		"tx_hash", txHash,
		"from", c.address.Hex(),
		"to", proxyWallet.Hex(),
		"gas_price", gasPrice.String(),
		"neg_risk", req.NegRisk,
		"condition_id", truncCondID(req.ConditionID))

	return txHash, nil
}

// doRelayPost performs a POST to relayer /submit with session cookie auth.
func (c *Client) doRelayPost(ctx context.Context, data []byte) (*RelaySubmitResponse, error) {
	fullURL := RelayerBaseURL + "/submit"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://polymarket.com")
	req.Header.Set("Referer", "https://polymarket.com/")

	// Authenticate via raw browser cookie string from environment.
	if cookies := os.Getenv("POLYMARKET_COOKIES"); cookies != "" {
		req.Header.Set("Cookie", cookies)
	}

	slog.Debug("http request", "method", "POST", "url", fullURL, "body_bytes", len(data),
		"has_cookies", os.Getenv("POLYMARKET_COOKIES") != "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("submit status %d: %s", resp.StatusCode, string(body))
	}

	var result RelaySubmitResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode submit response: %w (body: %s)", err, string(body))
	}
	return &result, nil
}

// HasSessionCredentials reports whether Polymarket session cookies are configured.
func (c *Client) HasSessionCredentials() bool {
	return os.Getenv("POLYMARKET_COOKIES") != ""
}

// PollRelayStatus polls the relay until the transaction reaches a final state.
// Returns (state, txHash, error). Final states: STATE_SUCCESS, STATE_FAILED, STATE_MINED.
// Times out after maxWait with the last known state.
func (c *Client) PollRelayStatus(ctx context.Context, txID string, maxWait time.Duration) (state, txHash string, err error) {
	fullURL := RelayerBaseURL + "/transaction/" + txID
	deadline := time.Now().Add(maxWait)

	for {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if reqErr != nil {
			return "", "", fmt.Errorf("create request: %w", reqErr)
		}
		req.Header.Set("Accept", "application/json")
		if cookies := os.Getenv("POLYMARKET_COOKIES"); cookies != "" {
			req.Header.Set("Cookie", cookies)
		}

		resp, doErr := c.httpClient.Do(req)
		if doErr != nil {
			slog.Warn("relay poll request failed", "tx_id", txID, "err", doErr)
		} else {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()

			slog.Info("relay poll response",
				"tx_id", txID,
				"status", resp.StatusCode,
				"body", string(body))

			if resp.StatusCode == http.StatusOK {
				var result RelaySubmitResponse
				if json.Unmarshal(body, &result) == nil {
					state = result.State
					txHash = result.TransactionHash
					switch state {
					case "STATE_SUCCESS", "STATE_MINED", "STATE_FAILED":
						return state, txHash, nil
					}
				}
			}
		}

		if time.Now().After(deadline) {
			return state, txHash, fmt.Errorf("poll timeout after %s, last state: %s", maxWait, state)
		}

		select {
		case <-ctx.Done():
			return state, txHash, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// Redeem redeems positions via the relayer using session cookie auth.
// It fetches the relay address and nonce from the relay API internally.
func (c *Client) Redeem(ctx context.Context, reqs []RedeemRequest) (*RelaySubmitResponse, error) {
	rp, err := c.GetRelayPayload(ctx)
	if err != nil {
		return nil, fmt.Errorf("get relay payload: %w", err)
	}
	return c.RedeemWithNonce(ctx, reqs, rp.Nonce, rp.Address)
}

// RedeemWithNonce redeems positions using a caller-provided nonce
// and relay address. This allows the caller to submit multiple individual
// transactions with locally-incrementing nonces without re-fetching the
// relay payload each time (which would return stale nonces).
func (c *Client) RedeemWithNonce(ctx context.Context, reqs []RedeemRequest, nonce, relayAddr string) (*RelaySubmitResponse, error) {
	if !c.HasSessionCredentials() {
		return nil, ErrNoSessionCredentials
	}
	if c.privateKey == nil {
		return nil, ErrNoPrivateKey
	}
	if len(reqs) == 0 {
		return nil, fmt.Errorf("no redeem requests provided")
	}

	// Build ABI-encoded calldata for all redeems in one proxy call.
	// Add a single setApprovalForAll if any request is neg-risk.
	var innerCalls []ProxyCallItem
	hasNegRisk := false
	for _, r := range reqs {
		if r.NegRisk && !hasNegRisk {
			hasNegRisk = true
			innerCalls = append(innerCalls, ProxyCallItem{
				Target: CTFAddress,
				Data:   EncodeSetApprovalForAll(NegRiskAdapterAddress, true),
			})
		}
	}
	for _, r := range reqs {
		if r.NegRisk {
			innerCalls = append(innerCalls, ProxyCallItem{
				Target: NegRiskAdapterAddress,
				Data:   EncodeNegRiskRedeem(r.ConditionID, r.Amounts),
			})
		} else {
			innerCalls = append(innerCalls, ProxyCallItem{
				Target: CTFAddress,
				Data:   EncodeRedeemPositions(r.ConditionID),
			})
		}
	}
	outerData := EncodeProxyMultiCall(innerCalls)

	// Estimate gas dynamically; falls back to static formula on failure.
	gasLimit := c.estimateGasLimit(ctx, outerData, len(innerCalls))

	// Sign the GSN meta-transaction.
	from := c.address
	to := proxyFactory
	sig, err := c.signRelayTx(from, to, outerData, "0", "0", gasLimit, nonce, RelayHubAddress, relayAddr)
	if err != nil {
		return nil, fmt.Errorf("sign relay tx: %w", err)
	}

	// Build the relay request.
	submitReq := RelaySubmitRequest{
		From:        from.Hex(),
		To:          to.Hex(),
		ProxyWallet: c.Funder().Hex(),
		Data:        "0x" + hex.EncodeToString(outerData),
		Nonce:       nonce,
		Signature:   sig,
		SignatureParams: RelaySignatureParams{
			GasPrice:   "0",
			GasLimit:   gasLimit,
			RelayerFee: "0",
			RelayHub:   RelayHubAddress,
			Relay:      relayAddr,
		},
		Type:     "PROXY",
		Metadata: "redeem",
	}

	data, err := json.Marshal(submitReq)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	slog.Debug("relay submit payload", "payload", string(data))

	return c.doRelayPost(ctx, data)
}

// --- ABI Encoding ---

// EncodeRedeemPositions builds the calldata for:
//   redeemPositions(address collateralToken, bytes32 parentCollectionId, bytes32 conditionId, uint256[] indexSets)
// with indexSets = [1, 2] (both YES and NO outcomes).
func EncodeRedeemPositions(conditionID string) []byte {
	conditionID = strings.TrimPrefix(conditionID, "0x")
	condBytes := common.Hex2Bytes(conditionID)

	var buf bytes.Buffer
	buf.Write(redeemSelector) // function selector

	// arg0: collateralToken (address, left-padded to 32 bytes)
	buf.Write(leftPad(common.HexToAddress(USDCeAddress).Bytes(), 32))
	// arg1: parentCollectionId (bytes32 = 0)
	buf.Write(make([]byte, 32))
	// arg2: conditionId (bytes32)
	buf.Write(rightPad(condBytes, 32))
	// arg3: offset to indexSets array (4 fixed args * 32 = 128 = 0x80)
	buf.Write(uint256(128))
	// indexSets array: length = 2, values = [1, 2]
	buf.Write(uint256(2))
	buf.Write(uint256(1))
	buf.Write(uint256(2))

	return buf.Bytes()
}

// EncodeNegRiskRedeem builds the calldata for NegRiskAdapter:
//
//	redeemPositions(bytes32 conditionId, uint256[] amounts)
//
// amounts = [yesAmount, noAmount] in on-chain units (1e6).
func EncodeNegRiskRedeem(conditionID string, amounts [2]*big.Int) []byte {
	conditionID = strings.TrimPrefix(conditionID, "0x")
	condBytes := common.Hex2Bytes(conditionID)

	var buf bytes.Buffer
	buf.Write(negRiskRedeemSelector) // function selector

	// arg0: conditionId (bytes32)
	buf.Write(rightPad(condBytes, 32))
	// arg1: offset to uint256[] = 0x40 (2 words)
	buf.Write(uint256(64))
	// array: length = 2
	buf.Write(uint256(2))
	// array[0] = yesAmount
	buf.Write(bigUint256(amounts[0]))
	// array[1] = noAmount
	buf.Write(bigUint256(amounts[1]))

	return buf.Bytes()
}

// EncodeSetApprovalForAll builds the calldata for:
//
//	setApprovalForAll(address operator, bool approved)
func EncodeSetApprovalForAll(operator string, approved bool) []byte {
	var buf bytes.Buffer
	buf.Write(setApprovalForAllSelector) // function selector
	buf.Write(leftPad(common.HexToAddress(operator).Bytes(), 32))
	if approved {
		buf.Write(uint256(1))
	} else {
		buf.Write(uint256(0))
	}
	return buf.Bytes()
}

// BuildRedeemCalls constructs the proxy call items for a redeem request.
// Regular markets: CTF.redeemPositions(USDC.e, 0, conditionId, [1,2])
// Neg-risk markets: CTF.setApprovalForAll(NegRiskAdapter, true)
//
//	+ NegRiskAdapter.redeemPositions(conditionId, amounts)
func BuildRedeemCalls(req RedeemRequest) []ProxyCallItem {
	if !req.NegRisk {
		return []ProxyCallItem{{
			Target: CTFAddress,
			Data:   EncodeRedeemPositions(req.ConditionID),
		}}
	}
	return []ProxyCallItem{
		{
			Target: CTFAddress,
			Data:   EncodeSetApprovalForAll(NegRiskAdapterAddress, true),
		},
		{
			Target: NegRiskAdapterAddress,
			Data:   EncodeNegRiskRedeem(req.ConditionID, req.Amounts),
		},
	}
}

// EncodeProxyCall builds the calldata for:
//   proxy((uint8 typeCode, address to, uint256 value, bytes data)[])
// with a single CALL to the target contract.
func EncodeProxyCall(targetAddr string, innerData []byte) []byte {
	return EncodeProxyMultiCall([]ProxyCallItem{{Target: targetAddr, Data: innerData}})
}

// ProxyCallItem describes one call within a proxy() batch.
type ProxyCallItem struct {
	Target string // contract address
	Data   []byte // calldata
}

// EncodeProxyMultiCall builds the calldata for:
//   proxy((uint8 typeCode, address to, uint256 value, bytes data)[])
// with multiple CALLs batched into one transaction.
func EncodeProxyMultiCall(calls []ProxyCallItem) []byte {
	var buf bytes.Buffer
	buf.Write(proxySelector) // function selector

	// Offset to calls array (one word).
	buf.Write(uint256(32))

	// Array length.
	buf.Write(uint256(len(calls)))

	// Each element in the array is a dynamic tuple, so we first write
	// an array of offsets, then the tuple data.
	// Compute offsets: each offset slot is 32 bytes.
	// Tuple data starts after len(calls) * 32 bytes of offsets.
	var tupleData [][]byte
	for _, c := range calls {
		tupleData = append(tupleData, encodeSingleProxyTuple(c))
	}

	// Write offsets.
	offset := len(calls) * 32
	for _, td := range tupleData {
		buf.Write(uint256(offset))
		offset += len(td)
	}

	// Write tuple data.
	for _, td := range tupleData {
		buf.Write(td)
	}

	return buf.Bytes()
}

// encodeSingleProxyTuple encodes one (typeCode, to, value, data) tuple.
func encodeSingleProxyTuple(c ProxyCallItem) []byte {
	var buf bytes.Buffer
	// typeCode = 1 (CALL)
	buf.Write(uint256(1))
	// to = target contract address
	buf.Write(leftPad(common.HexToAddress(c.Target).Bytes(), 32))
	// value = 0
	buf.Write(uint256(0))
	// offset to bytes data (4 tuple fields * 32 = 128)
	buf.Write(uint256(128))

	// bytes data: length + data + padding
	buf.Write(uint256(len(c.Data)))
	buf.Write(c.Data)
	if pad := len(c.Data) % 32; pad != 0 {
		buf.Write(make([]byte, 32-pad))
	}

	return buf.Bytes()
}

// --- GSN v1 Signing ---

// signRelayTx signs a GSN v1 meta-transaction for the Polymarket proxy wallet.
//
// The signed message is:
//   keccak256(encodePacked("rlx:", from, to, data, txFee, gasPrice, gasLimit, nonce, relayHub, relay))
// Then wrapped with personal_sign prefix: "\x19Ethereum Signed Message:\n32" + hash.
func (c *Client) signRelayTx(
	from, to common.Address,
	data []byte,
	txFee, gasPrice, gasLimit, nonce string,
	relayHub, relay string,
) (string, error) {
	// Build the packed data for hashing.
	var packed bytes.Buffer
	packed.WriteString("rlx:")       // 4-byte prefix
	packed.Write(from.Bytes())       // 20 bytes
	packed.Write(to.Bytes())         // 20 bytes
	packed.Write(data)               // variable length
	packed.Write(uint256FromStr(txFee))
	packed.Write(uint256FromStr(gasPrice))
	packed.Write(uint256FromStr(gasLimit))
	packed.Write(uint256FromStr(nonce))
	packed.Write(common.HexToAddress(relayHub).Bytes()) // 20 bytes
	packed.Write(common.HexToAddress(relay).Bytes())    // 20 bytes

	// Hash the packed data.
	txHash := crypto.Keccak256(packed.Bytes())

	slog.Debug("relay sign debug",
		"packed_len", packed.Len(),
		"tx_hash", hex.EncodeToString(txHash),
		"from", from.Hex(),
		"to", to.Hex(),
		"data_len", len(data),
		"nonce", nonce,
		"gas_limit", gasLimit,
		"relay", relay)

	// Wrap with Ethereum personal_sign prefix.
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(txHash))
	personalHash := crypto.Keccak256(append([]byte(prefix), txHash...))

	// ECDSA sign.
	sig, err := crypto.Sign(personalHash, c.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}

	// Verify: recover signer address from signature.
	recoveredPub, err := crypto.Ecrecover(personalHash, sig)
	if err == nil {
		pubKey, _ := crypto.UnmarshalPubkey(recoveredPub)
		if pubKey != nil {
			recovered := crypto.PubkeyToAddress(*pubKey)
			slog.Debug("relay sign verify",
				"expected", from.Hex(),
				"recovered", recovered.Hex(),
				"match", recovered == from)
		}
	}

	// Convert V from 0/1 to 27/28.
	sig[64] += 27

	return "0x" + hex.EncodeToString(sig), nil
}

// calculateGasLimit computes the gas limit using a static formula as fallback.
func calculateGasLimit(txCount int) string {
	return fmt.Sprintf("%d", gasRelayBase+txCount*gasPerRedeem)
}

// estimateGasLimit estimates the gas needed for a proxy call by simulating
// it on-chain via eth_estimateGas, then adds a 30% buffer.
// Falls back to the static formula if estimation fails.
func (c *Client) estimateGasLimit(ctx context.Context, outerData []byte, callCount int) string {
	rpcURL := c.polygonRPC
	if rpcURL == "" {
		rpcURL = DefaultPolygonRPC
	}

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		slog.Warn("gas estimate: rpc dial failed, using static", "err", err)
		return calculateGasLimit(callCount)
	}
	defer client.Close()

	// Simulate calling proxy() on the proxy wallet from the EOA.
	proxyWallet := c.Funder()
	estimated, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From: c.address,
		To:   &proxyWallet,
		Data: outerData,
	})
	if err != nil {
		slog.Warn("gas estimate: estimation failed, using static", "err", err)
		return calculateGasLimit(callCount)
	}

	// Add 30% buffer for relay hub overhead and safety margin.
	buffered := estimated * 130 / 100
	slog.Info("gas estimate",
		"estimated", estimated,
		"buffered", buffered,
		"static", gasRelayBase+callCount*gasPerRedeem)

	return fmt.Sprintf("%d", buffered)
}

// --- Helpers ---

// leftPad pads data with leading zeros to the target length.
func leftPad(data []byte, size int) []byte {
	if len(data) >= size {
		return data[:size]
	}
	padded := make([]byte, size)
	copy(padded[size-len(data):], data)
	return padded
}

// rightPad pads data with trailing zeros to the target length.
func rightPad(data []byte, size int) []byte {
	if len(data) >= size {
		return data[:size]
	}
	padded := make([]byte, size)
	copy(padded, data)
	return padded
}

// uint256 encodes an integer as a 32-byte big-endian value.
func uint256(v int) []byte {
	return common.LeftPadBytes(big.NewInt(int64(v)).Bytes(), 32)
}

// bigUint256 encodes a *big.Int as a 32-byte big-endian value.
func bigUint256(v *big.Int) []byte {
	if v == nil {
		return make([]byte, 32)
	}
	return common.LeftPadBytes(v.Bytes(), 32)
}

// uint256FromStr encodes a decimal string as a 32-byte big-endian value.
func uint256FromStr(s string) []byte {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return make([]byte, 32)
	}
	return common.LeftPadBytes(n.Bytes(), 32)
}

// mustHexDecode decodes a hex string, panicking on error (used for constants).
func mustHexDecode(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("invalid hex: " + s)
	}
	return b
}

// truncID truncates a hex ID for log output, showing the first 8 and last 4 characters.
func truncID(id string) string {
	if len(id) <= 16 {
		return id
	}
	return id[:8] + "..." + id[len(id)-4:]
}

// truncCondID truncates a condition ID for log output, showing the first 16 characters.
func truncCondID(id string) string {
	if len(id) <= 20 {
		return id
	}
	return id[:16] + "..."
}

// readRelayHubNonce queries the relay hub contract on-chain for the EOA's current nonce.
// The relay hub tracks nonces[from] globally; this is the authoritative value for signing.
func (c *Client) readRelayHubNonce(ctx context.Context) (string, error) {
	rpcURL := c.polygonRPC
	if rpcURL == "" {
		rpcURL = DefaultPolygonRPC
	}
	return c.readRelayHubNonceVia(ctx, rpcURL)
}

// readRelayHubNonceVia queries the relay hub contract on-chain for the EOA's current nonce
// using the specified RPC URL.
func (c *Client) readRelayHubNonceVia(ctx context.Context, rpcURL string) (string, error) {
	slog.Info("relay: reading on-chain nonce",
		"rpc", rpcURL,
		"relay_hub", RelayHubAddress,
		"eoa", c.address.Hex())

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return "", fmt.Errorf("dial polygon rpc: %w", err)
	}
	defer client.Close()

	// Function selector: bytes4(keccak256("getNonce(address)")) = 0x2d0335ab
	sel := crypto.Keccak256([]byte("getNonce(address)"))[:4]
	arg := common.LeftPadBytes(c.address.Bytes(), 32)
	callData := append(sel, arg...)

	relayHubAddr := common.HexToAddress(RelayHubAddress)
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &relayHubAddr,
		Data: callData,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("call relay hub: %w", err)
	}
	if len(result) < 32 {
		return "", fmt.Errorf("unexpected result length: %d", len(result))
	}

	nonce := new(big.Int).SetBytes(result[:32])
	slog.Info("relay: on-chain nonce read ok",
		"nonce", nonce.String(),
		"raw_hex", hex.EncodeToString(result[:32]))
	return nonce.String(), nil
}

// CheckTxReceipt queries the on-chain transaction receipt and returns the status.
// Returns 1 for success, 0 for revert, or an error if the receipt is not yet available.
func (c *Client) CheckTxReceipt(ctx context.Context, txHash string) (uint64, error) {
	rpcURL := c.polygonRPC
	if rpcURL == "" {
		rpcURL = DefaultPolygonRPC
	}

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return 0, fmt.Errorf("dial polygon rpc: %w", err)
	}
	defer client.Close()

	hash := common.HexToHash(txHash)
	receipt, err := client.TransactionReceipt(ctx, hash)
	if err != nil {
		return 0, fmt.Errorf("get receipt: %w", err)
	}
	return receipt.Status, nil
}
