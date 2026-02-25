package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/easay/pmbot/internal/backtest"
	"github.com/easay/pmbot/internal/clob"
	"github.com/easay/pmbot/internal/config"
	"github.com/easay/pmbot/internal/engine"
	"github.com/easay/pmbot/internal/logger"
	"github.com/easay/pmbot/internal/strategy"
)

// Build-time variables injected via -ldflags.
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("pmbot %s (commit=%s built=%s)\n", version, commit, buildTime)
		os.Exit(0)
	}

	logger.Init(slog.LevelInfo, "")

	// Load configuration.
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config failed", "err", err)
		os.Exit(1)
	}
	slog.Info("config loaded", "event", cfg.Market.EventSlug, "strategy", cfg.Strategy.Name)

	// Private key from environment.
	privateKey := os.Getenv("POLYMARKET_PRIVATE_KEY")
	if privateKey == "" {
		fmt.Println("POLYMARKET_PRIVATE_KEY environment variable is required.")
		fmt.Println("Example:")
		fmt.Println("  export POLYMARKET_PRIVATE_KEY=your_hex_private_key")
		fmt.Println("  go run ./cmd/pmbot/ -config config.yaml")
		os.Exit(1)
	}

	// Create authenticated CLOB client.
	opts := []clob.Option{
		clob.WithPrivateKey(privateKey),
	}
	if polyAddr := os.Getenv("POLYMARKET_WALLET_ADDRESS"); polyAddr != "" {
		if !isValidHexAddress(polyAddr) {
			slog.Error("invalid POLYMARKET_WALLET_ADDRESS", "addr", polyAddr)
			os.Exit(1)
		}
		opts = append(opts, clob.WithPolymarketAddress(polyAddr))
	}
	// Optional: override auto-detected signature type (0=EOA, 1=POLY_PROXY, 2=GNOSIS_SAFE).
	if sigTypeStr := os.Getenv("POLYMARKET_SIGNATURE_TYPE"); sigTypeStr != "" {
		sigType, err := strconv.Atoi(sigTypeStr)
		if err != nil || sigType < 0 || sigType > 2 {
			slog.Error("invalid POLYMARKET_SIGNATURE_TYPE (must be 0, 1, or 2)", "value", sigTypeStr)
			os.Exit(1)
		}
		opts = append(opts, clob.WithSignatureType(clob.SignatureType(sigType)))
	}
	// Polygon RPC for on-chain nonce queries (relay hub authoritative nonce).
	// Prefer POLYMARKET_POLYGON_RPC; fall back to feed.chainlink_rpc if set.
	polygonRPC := os.Getenv("POLYMARKET_POLYGON_RPC")
	if polygonRPC == "" {
		polygonRPC = cfg.Feed.ChainlinkRPC
	}
	if polygonRPC != "" {
		opts = append(opts, clob.WithPolygonRPC(polygonRPC))
		slog.Info("polygon rpc configured for on-chain nonce", "rpc", polygonRPC)
	}
	clobClient, err := clob.NewClient(opts...)
	if err != nil {
		slog.Error("create clob client failed", "err", err)
		os.Exit(1)
	}
	slog.Info("wallet initialized", "address", clobClient.Address().Hex(), "funder", clobClient.Funder().Hex(), "sig_type", clobClient.SigType())

	// Preflight: verify relay nonce is readable at startup.
	{
		prefCtx, prefCancel := context.WithTimeout(context.Background(), 10*time.Second)
		rp, err := clobClient.GetRelayPayload(prefCtx)
		prefCancel()
		if err != nil {
			slog.Warn("relay preflight failed", "err", err)
		} else {
			slog.Info("relay preflight ok", "nonce", rp.Nonce, "relay", rp.Address)
		}
	}

	// Authenticate: create or derive API key.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	creds, err := clobClient.CreateOrDeriveAPIKey(ctx)
	if err != nil {
		slog.Error("create api key failed", "err", err)
		os.Exit(1)
	}
	slog.Info("api key ready", "key", creds.Key[:8]+"...")

	// Build slots from entry_prices (required for btc_updown).
	prices, ok := cfg.Strategy.Params["entry_prices"]
	if !ok || prices == "" {
		slog.Error("strategy.params.entry_prices is required (comma-separated, e.g. 0.90,0.95)")
		os.Exit(1)
	}
	// live_prices: subset of entry_prices that place real orders (when dry_run=false).
	// If empty, all slots are paper-only regardless of dry_run.
	livePrices := cfg.Strategy.Params["live_prices"]
	paperSlots, err := buildPaperSlots(cfg.Strategy, prices, livePrices)
	if err != nil {
		slog.Error("build paper slots failed", "err", err)
		os.Exit(1)
	}
	var liveCount int
	for _, s := range paperSlots {
		if s.Live {
			liveCount++
		}
	}
	trendConfirm := cfg.Strategy.Params["trend_confirm"]
	if trendConfirm == "" {
		trendConfirm = "1m"
	}
	slog.Info("slots configured", "total", len(paperSlots), "live", liveCount, "trend_confirm", trendConfirm)

	// Start engine.
	eng := engine.New(cfg, clobClient, paperSlots)

	// Register backtest handler at /backtest.
	if st := eng.Store(); st != nil {
		eng.SetExtraHandler(backtest.NewHandler(st, cfg.Strategy))
	}

	if err := eng.Run(ctx); err != nil {
		slog.Error("engine error", "err", err)
		os.Exit(1)
	}

	slog.Info("shutdown complete")
}

// buildStrategy creates a Strategy from configuration.
func buildStrategy(cfg config.StrategyConfig) (strategy.Strategy, error) {
	return strategy.BuildFromConfig(cfg)
}

// buildPaperSlots creates slots from entry_prices.
// livePricesCSV specifies which prices place real orders (empty = all paper-only).
func buildPaperSlots(cfg config.StrategyConfig, pricesCSV, livePricesCSV string) ([]engine.PaperSlotConfig, error) {
	// Parse live prices into a set for fast lookup.
	liveSet := make(map[string]bool)
	for _, p := range strings.Split(livePricesCSV, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			liveSet[p] = true
		}
	}

	parts := strings.Split(pricesCSV, ",")
	var slots []engine.PaperSlotConfig
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		price, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil, fmt.Errorf("parse entry_prices value %q: %w", p, err)
		}
		// Clone config params with this specific entry price.
		slotCfg := config.StrategyConfig{
			Name:   cfg.Name,
			Params: make(map[string]string, len(cfg.Params)),
		}
		for k, v := range cfg.Params {
			slotCfg.Params[k] = v
		}
		slotCfg.Params["entry_price"] = p
		strat, err := buildStrategy(slotCfg)
		if err != nil {
			return nil, fmt.Errorf("build strategy for price %s: %w", p, err)
		}
		slots = append(slots, engine.PaperSlotConfig{
			EntryPrice: price,
			Live:       liveSet[p],
			Strategy:   strat,
		})
	}
	if len(slots) == 0 {
		return nil, fmt.Errorf("entry_prices is empty")
	}
	return slots, nil
}

var hexAddrRe = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// isValidHexAddress checks if s is a valid Ethereum hex address.
func isValidHexAddress(s string) bool {
	return hexAddrRe.MatchString(s)
}
