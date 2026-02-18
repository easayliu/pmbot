package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	DryRun           bool            `yaml:"dry_run"`
	ReportAddr       string          `yaml:"report_addr"`        // HTTP report server address (default ":8686")
	MaxPaperSessions int             `yaml:"max_paper_sessions"` // max historical paper sessions to keep (default 10)
	Market           MarketConfig    `yaml:"market"`
	Feed             FeedConfig      `yaml:"feed"`
	Strategy         StrategyConfig  `yaml:"strategy"`
	Execution        ExecutionConfig `yaml:"execution"`
}

// ExecutionConfig controls live order execution parameters.
// All fields have sensible defaults; omitting the section entirely is safe.
type ExecutionConfig struct {
	MaxDailyOrders    int     `yaml:"max_daily_orders"`     // circuit breaker: max orders per day (default 50)
	MaxDailyAmount    float64 `yaml:"max_daily_amount"`     // circuit breaker: max USDC per day (default 1000)
	MaxWindowRetries  int     `yaml:"max_window_retries"`   // max FAK retries per slot per window (default 3)
	RetryCooldownSec  float64 `yaml:"retry_cooldown_sec"`   // seconds between FAK retries (default 1)
	MaxSlippagePct    float64 `yaml:"max_slippage_pct"`     // max price deviation from signal (default 0.03 = 3%)
	FAKPriceBufferTks int     `yaml:"fak_price_buffer_ticks"` // ticks above best ask for FAK BUY (default 2)
}

// RetryCooldown returns the retry cooldown as a time.Duration.
func (e ExecutionConfig) RetryCooldown() time.Duration {
	return time.Duration(e.RetryCooldownSec * float64(time.Second))
}

// MarketConfig identifies the Polymarket event and market to trade.
type MarketConfig struct {
	EventSlug string `yaml:"event_slug"`
	Timezone  string `yaml:"timezone"` // IANA timezone for {today} resolution (default: America/New_York)
}

// FeedConfig controls external data feed parameters.
// When polymarket_oracle is true (recommended), all price data comes from
// Polymarket's RTDS Chainlink feed — the exact oracle used for 5m Up/Down
// resolution. This ensures price, trend, and candle direction all match
// the settlement source, eliminating data source mismatch.
// Binance is available as a fallback when polymarket_oracle is false.
type FeedConfig struct {
	PolymarketOracle  bool   `yaml:"polymarket_oracle"`   // use Polymarket RTDS Chainlink (recommended)
	BinanceSymbol     string `yaml:"binance_symbol"`      // fallback: Binance price source
	PollIntervalSec   int    `yaml:"poll_interval_sec"`
	ChainlinkRPC      string `yaml:"chainlink_rpc"`       // optional: on-chain Chainlink
	ChainlinkContract string `yaml:"chainlink_contract"`  // required if chainlink_rpc is set
}

// PollInterval returns the polling interval as a time.Duration.
func (f FeedConfig) PollInterval() time.Duration {
	if f.PollIntervalSec <= 0 {
		return 5 * time.Second
	}
	return time.Duration(f.PollIntervalSec) * time.Second
}

// StrategyConfig holds the strategy name and its parameters.
type StrategyConfig struct {
	Name   string            `yaml:"name"`
	Params map[string]string `yaml:"params"`
}

// Load reads a YAML config file and returns the parsed Config.
// The POLYMARKET_PRIVATE_KEY environment variable is not part of the config
// file—it is read separately by the caller.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	// For dynamic slug templates ({5m_window}), keep the raw template for the engine
	// to re-resolve at each window. For static templates ({today}), resolve once.
	if !Has5mWindow(cfg.Market.EventSlug) {
		slug, err := ResolveSlug(cfg.Market.EventSlug, cfg.Market.Timezone)
		if err != nil {
			return nil, fmt.Errorf("resolve event_slug: %w", err)
		}
		cfg.Market.EventSlug = slug
	}
	if cfg.Market.EventSlug == "" {
		return nil, fmt.Errorf("market.event_slug is required")
	}
	// Default report address.
	if cfg.ReportAddr == "" {
		cfg.ReportAddr = ":8686"
	}
	// Default max paper sessions.
	if cfg.MaxPaperSessions <= 0 {
		cfg.MaxPaperSessions = 10
	}
	// Default to Polymarket RTDS Chainlink feed (matches settlement oracle).
	// Only fall back to Binance if explicitly configured with polymarket_oracle=false.
	if !cfg.Feed.PolymarketOracle && cfg.Feed.BinanceSymbol == "" && cfg.Feed.ChainlinkRPC == "" {
		cfg.Feed.PolymarketOracle = true
	}
	// Validate Chainlink config completeness.
	if cfg.Feed.ChainlinkRPC != "" && cfg.Feed.ChainlinkContract == "" {
		return nil, fmt.Errorf("feed.chainlink_contract is required when chainlink_rpc is set")
	}

	// Default execution parameters.
	if cfg.Execution.MaxDailyOrders <= 0 {
		cfg.Execution.MaxDailyOrders = 50
	}
	if cfg.Execution.MaxDailyAmount <= 0 {
		cfg.Execution.MaxDailyAmount = 1000.0
	}
	if cfg.Execution.MaxWindowRetries <= 0 {
		cfg.Execution.MaxWindowRetries = 3
	}
	if cfg.Execution.RetryCooldownSec <= 0 {
		cfg.Execution.RetryCooldownSec = 1.0
	}
	if cfg.Execution.MaxSlippagePct <= 0 {
		cfg.Execution.MaxSlippagePct = 0.03
	}
	if cfg.Execution.FAKPriceBufferTks <= 0 {
		cfg.Execution.FAKPriceBufferTks = 2
	}

	return &cfg, nil
}

// ResolveSlug resolves all template variables in the event slug.
// Supported templates:
//
//	{today}      → "month-day" (e.g., "february-19")
//	{5m_window}  → Unix timestamp of the current 5-minute window start (floor-aligned)
func ResolveSlug(slug, timezone string) (string, error) {
	if timezone == "" {
		timezone = "America/New_York"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return "", fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}
	now := time.Now().In(loc)

	if strings.Contains(slug, "{today}") {
		month := strings.ToLower(now.Month().String())
		day := now.Day()
		slug = strings.ReplaceAll(slug, "{today}", fmt.Sprintf("%s-%d", month, day))
	}

	if strings.Contains(slug, "{5m_window}") {
		// Align to current 5-minute window start (floor).
		// Polymarket slug uses window START time, not end/resolution time.
		// e.g., at 12:03 → floor=12:00 → slug points to the 12:00-12:05 market.
		windowStart := now.Unix() / 300 * 300
		slug = strings.ReplaceAll(slug, "{5m_window}", fmt.Sprintf("%d", windowStart))
	}

	return slug, nil
}

// Has5mWindow returns true if the slug template contains {5m_window}.
func Has5mWindow(slug string) bool {
	return strings.Contains(slug, "{5m_window}")
}
