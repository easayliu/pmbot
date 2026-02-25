package feed

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ABI function selectors for Chainlink AggregatorV3Interface.
var (
	// latestRoundData() returns (uint80,int256,uint256,uint256,uint80)
	latestRoundDataSelector = common.Hex2Bytes("feaf968c")
	// decimals() returns (uint8)
	decimalsSelector = common.Hex2Bytes("313ce567")
)

// ChainlinkFeed polls a Chainlink price feed aggregator contract via Ethereum RPC.
type ChainlinkFeed struct {
	rpcURL   string
	contract common.Address
	interval time.Duration
	ticksCh  chan PriceTick
	decimals uint8
}

// NewChainlinkFeed creates a feed that polls Chainlink oracle for BTC/USD price.
func NewChainlinkFeed(rpcURL, contractAddr string, pollInterval time.Duration) *ChainlinkFeed {
	return &ChainlinkFeed{
		rpcURL:   rpcURL,
		contract: common.HexToAddress(contractAddr),
		interval: pollInterval,
		ticksCh:  make(chan PriceTick, 256),
	}
}

// Ticks returns the channel that receives price ticks.
func (f *ChainlinkFeed) Ticks() <-chan PriceTick {
	return f.ticksCh
}

// Run connects to the RPC endpoint and polls the oracle until ctx is cancelled.
// It automatically reconnects with exponential backoff on error.
func (f *ChainlinkFeed) Run(ctx context.Context) {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := f.poll(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("chainlink feed error, reconnecting", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		} else {
			// Reset backoff after successful connection.
			backoff = 1 * time.Second
		}
	}
}

// poll establishes an RPC connection and polls latestRoundData in a loop.
func (f *ChainlinkFeed) poll(ctx context.Context) error {
	client, err := ethclient.DialContext(ctx, f.rpcURL)
	if err != nil {
		return fmt.Errorf("dial %s: %w", f.rpcURL, err)
	}
	defer client.Close()

	// Fetch decimals once per connection.
	if f.decimals == 0 {
		dec, err := f.fetchDecimals(ctx, client)
		if err != nil {
			return fmt.Errorf("fetch decimals: %w", err)
		}
		f.decimals = dec
		slog.Info("chainlink feed connected", "contract", f.contract.Hex(), "decimals", f.decimals, "interval", f.interval)
	}

	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()

	// Fetch immediately on connect, then on each tick.
	for {
		price, updatedAt, err := f.fetchLatestPrice(ctx, client)
		if err != nil {
			return fmt.Errorf("fetch price: %w", err)
		}

		tick := PriceTick{
			Symbol: "BTC/USD",
			Price:  price,
			Time:   updatedAt,
		}

		select {
		case f.ticksCh <- tick:
		default:
			// Drop oldest, push newest.
			<-f.ticksCh
			f.ticksCh <- tick
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// fetchDecimals calls decimals() on the aggregator contract.
func (f *ChainlinkFeed) fetchDecimals(ctx context.Context, client *ethclient.Client) (uint8, error) {
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &f.contract,
		Data: decimalsSelector,
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("call decimals(): %w", err)
	}
	if len(result) < 32 {
		return 0, fmt.Errorf("unexpected decimals response length: %d", len(result))
	}
	return uint8(new(big.Int).SetBytes(result).Uint64()), nil
}

// fetchLatestPrice calls latestRoundData() and returns (price, updatedAt).
// Response ABI layout (5 × 32 bytes):
//
//	[0:32]   uint80  roundId
//	[32:64]  int256  answer (price with `decimals` decimal places)
//	[64:96]  uint256 startedAt
//	[96:128] uint256 updatedAt
//	[128:160] uint80 answeredInRound
func (f *ChainlinkFeed) fetchLatestPrice(ctx context.Context, client *ethclient.Client) (float64, time.Time, error) {
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &f.contract,
		Data: latestRoundDataSelector,
	}, nil)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("call latestRoundData(): %w", err)
	}
	if len(result) < 160 {
		return 0, time.Time{}, fmt.Errorf("unexpected response length: %d (expected 160)", len(result))
	}

	// Decode int256 answer at offset 32.
	answer := new(big.Int).SetBytes(result[32:64])
	if result[32]&0x80 != 0 {
		// Two's complement for negative int256.
		two256 := new(big.Int).Lsh(big.NewInt(1), 256)
		answer.Sub(answer, two256)
	}

	// Decode uint256 updatedAt at offset 96.
	updatedAt := new(big.Int).SetBytes(result[96:128])

	// Convert to float64 with decimal scaling.
	divisor := math.Pow10(int(f.decimals))
	price := float64(answer.Int64()) / divisor

	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, time.Time{}, fmt.Errorf("invalid price from oracle: %v (raw answer=%s)", price, answer.String())
	}

	return price, time.Unix(updatedAt.Int64(), 0), nil
}
