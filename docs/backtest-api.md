# Backtest API Documentation

## Page Routes

| Path | Type | Description |
|------|------|-------------|
| `/` | HTML | Navigation home — links to Paper / Backtest / Data |
| `/paper` | HTML | Paper Trading live page (SPA) |
| `/backtest` | HTML | Backtest page (SPA shell, data loaded via API) |
| `/data` | HTML | Raw data report (ask/bid price tables, arbitrage analysis, etc.) |

## Paper Trading API

| Path | Method | Description |
|------|--------|-------------|
| `/api/paper/data` | GET | Paper Trading JSON data |
| `/api/paper/stream` | GET | Paper Trading SSE real-time push |

---

## Backtest API

### `GET /api/backtest/data`

Core backtest endpoint. Supports 4 run modes. All parameters are query strings.

### Common Parameters

| Parameter | Format | Description | Example |
|-----------|--------|-------------|---------|
| `from` | `2006-01-02` or RFC3339 | Start date (inclusive), empty = unbounded | `2025-03-01` |
| `to` | `2006-01-02` or RFC3339 | End date (inclusive, auto-extended to 23:59:59) | `2025-03-15` |
| `params` | `key=val,key=val` | Override strategy params from config.yaml | `vol_sigma=2.0,min_threshold=10` |

---

### Mode 1: Standard Backtest (no sweep)

Run with current config (+ params overrides). If `entry_prices` contains multiple comma-separated prices, each price level runs independently and returns separate result rows.

```
GET /api/backtest/data
GET /api/backtest/data?params=entry_prices=0.95,0.90,0.85
GET /api/backtest/data?from=2025-03-01&to=2025-03-10
```

### Mode 2: Parameter Sweep

| Parameter | Format | Description |
|-----------|--------|-------------|
| `sweep` | `key=start:end:step,key=start:end:step` | Parameter grid search |

**Shorthand aliases:**

| Alias | Full Key |
|-------|----------|
| `ep` | `entry_price` |
| `vs` | `vol_sigma` |
| `fve` | `fair_value_edge` |
| `mss` | `min_signal_strength` |
| `tt` | `trend_threshold` |
| `mes` | `min_elapsed_sec` |
| `mt` | `min_threshold` |
| `adv` | `accel_decay_vol` |
| `td` | `trend_discount` |
| `lws` | `late_window_sec` |
| `lwtm` | `late_window_threshold_mul` |
| `mrs` | `mean_rev_sigma` |
| `sl` | `streak_len` |
| `sd` | `streak_discount` |
| `ms` | `min_spread` |

```bash
# Sweep entry_price from 0.90 to 0.99, step 0.01
GET /api/backtest/data?sweep=ep=0.90:0.99:0.01

# 2D sweep: entry_price × vol_sigma
GET /api/backtest/data?sweep=ep=0.90:0.95:0.01,vs=1.0:2.0:0.5

# Spread strategy sweep
GET /api/backtest/data?sweep=ms=0.10:0.40:0.05,ep=0.80:0.95:0.05
```

Results are sorted by `totalPnL` descending. The first row is the best parameter combination.

### Mode 3: Train/Test Split Validation

| Parameter | Format | Description |
|-----------|--------|-------------|
| `split` | `0.0~1.0` | Training set ratio, e.g. `0.7` = 70% train / 30% test |

Can be combined with `sweep`. Without `sweep`, runs a single split validation on current config.

```bash
# Single config 70/30 split
GET /api/backtest/data?split=0.7

# Sweep + split validation (detect overfitting)
GET /api/backtest/data?sweep=ep=0.90:0.99:0.01&split=0.7
```

Response includes OOS (out-of-sample) metrics: `oosTrades`, `oosWinRate`, `oosPnL`, `oosSharpe`, `pnlDegradation`.

### Mode 4: Walk-Forward Rolling Validation

| Parameter | Format | Description |
|-----------|--------|-------------|
| `wf` | `trainSize:testSize:stepSize` | Rolling window counts (unit: number of 5-minute windows) |

**Must** be used together with `sweep` (each fold runs a sweep to find best params).

```bash
# 100 windows train, 50 windows test, roll by 50
GET /api/backtest/data?sweep=ep=0.90:0.99:0.01&wf=100:50:50
```

Response includes: per-fold best param label, train PnL, test PnL, parameter stability percentage.

---

## JSON Response Structure

```jsonc
{
  "meta": {
    "title": "Backtest Results",           // page title
    "period": "2025-02-20 ~ 2025-03-03",   // data date range
    "windowCount": 1500,                    // total 5-minute windows
    "timestamp": "2025-03-03 12:00:00",     // computation time
    "dryRun": false                         // engine dry-run mode
  },
  "form": {
    "sweepSpec": "ep=0.90:0.99:0.01",      // echo back query params
    "splitStr": "",
    "wfSpec": "",
    "fromStr": "",
    "toStr": "",
    "paramsSpec": "",
    "paramGroups": [                        // config panel groups
      {
        "name": "Late Sniper",
        "params": [
          {
            "key": "late_window_sec",
            "label": "Window (s)",
            "value": "",                    // current override (empty = using base)
            "placeholder": "60",            // base config value
            "toggleKey": true               // 0 = disabled
          }
        ]
      }
    ]
  },
  "livePaper": {                            // live engine stats (if running)
    "hasLive": false,
    "hasPaper": true,
    "live": {
      "trades": 0, "wins": 0, "losses": 0,
      "resolved": 0, "totalPnL": 0, "winRate": 0,
      "avgPnL": 0, "hasResolved": false
    },
    "paper": {
      "trades": 50, "wins": 30, "losses": 20,
      "resolved": 50, "totalPnL": 12.50, "winRate": 60.0,
      "avgPnL": 0.25, "hasResolved": true
    }
  },
  "summary": {                              // best-of summary cards
    "configCount": 10,                      // number of param combinations tested
    "totalTrades": 500,                     // total trades across all configs
    "bestPnL": 25.50,
    "bestWinRate": 65.0,
    "bestSharpe": 2.1,
    "bestExpectancy": 0.05
  },
  "split": {                                // only in split mode
    "hasSplit": true,
    "splitRatio": 0.7,
    "trainWindows": 1050,
    "testWindows": 450
  },
  "results": [                              // standard & split mode
    {
      "index": 1,
      "label": "ep=0.95",                  // param combination label
      "trades": 120,
      "wins": 78,
      "winRate": 65.0,
      "totalPnL": 25.50,
      "sharpe": 2.1,
      "maxDD": -5.20,
      "profitFactor": "1.85",              // string, "—" when N/A
      "expectancy": 0.05,
      "avgWin": 0.12,
      "avgLoss": -0.08,
      "winLossRatio": 1.50,
      "maxConsecWins": 8,
      "maxConsecLoss": 4,
      "isBest": true,
      // split mode extra fields:
      "oosTrades": 40,
      "oosWins": 24,
      "oosWinRate": 60.0,
      "oosPnL": 8.20,
      "oosSharpe": 1.5,
      "oosExpectancy": 0.04,
      "pnlDegradation": 15.5              // (trainPnL - testPnL) / trainPnL * 100
    }
  ],
  "walkForward": {                          // only in wf mode
    "trainSize": 100,
    "testSize": 50,
    "stepSize": 50,
    "foldCount": 5,
    "totalOOSTrades": 200,
    "totalOOSPnL": 12.30,
    "avgOOSWinRate": 58.0,
    "avgOOSSharpe": 1.2,
    "paramStability": 60.0,                // % of folds where best params match previous fold
    "folds": [
      {
        "index": 1,
        "trainPeriod": "02-20 00:00 ~ 02-25 12:00",
        "testPeriod": "02-25 12:00 ~ 02-28 00:00",
        "bestLabel": "ep=0.95 vs=1.5",
        "trainPnL": 15.00,
        "testTrades": 40,
        "testWins": 24,
        "testWinRate": 60.0,
        "testPnL": 3.20,
        "testSharpe": 1.1,
        "paramStable": false
      }
    ]
  },
  "config": {                               // copy-paste ready YAML
    "yaml": "strategy:\n  name: \"btc_updown\"\n  params:\n    ..."
  }
}
```

---

## Strategy Parameters

### btc_updown Strategy

| Parameter | Type | Description |
|-----------|------|-------------|
| `max_cost` | float | Max cost per trade (USDC) |
| `entry_prices` | string | Comma-separated entry price caps, e.g. `"0.95,0.90,0.85"` |
| `vol_sigma` | float | Volatility threshold multiplier (0 = disabled, uses fixed `trend_threshold`) |
| `min_threshold` | float | Absolute floor for vol-adjusted threshold ($) |
| `trend_threshold` | float | Fixed trend threshold (used when `vol_sigma=0`) |
| `min_elapsed_sec` | float | Minimum seconds into window before trading |
| `min_elapsed_floor_sec` | float | Absolute floor for adaptive elapsed (seconds) |
| `elapsed_price_ref` | float | Entry price elapsed scaling reference (0 = disabled) |
| `trend_confirm` | string | Trend confirmation window: `"1m"` or `"5m"` |
| `trend_discount` | float | Threshold discount when trend confirms (0.0~1.0, 1.0 = disabled) |
| `accel_decay_vol` | float | Momentum decay detection sigma (0 = disabled) |
| `late_window_sec` | float | Activate sniper mode in last N seconds (0 = disabled) |
| `late_window_threshold_mul` | float | Sniper mode threshold multiplier (default 0.3) |
| `mean_rev_sigma` | float | Mean reversion trigger sigma (0 = disabled) |
| `mean_rev_max_elapsed_sec` | float | Mean reversion only active in first N seconds |
| `streak_len` | int | Consecutive same-direction windows to trigger reversal bias (0 = disabled) |
| `streak_discount` | float | Threshold discount for counter-streak direction |
| `min_signal_strength` | float | Minimum signal strength \|change\| / threshold (0 = disabled) |
| `fair_value_edge` | float | Fair value gate: fair_value - ask >= edge (0 = disabled) |
| `early_exit_stop_factor` | float | Stop-loss: exit when FV < entry × factor (0 = disabled) |
| `early_exit_min_hold_sec` | float | Minimum hold seconds before stop-loss can trigger |

### spread Strategy

| Parameter | Type | Description |
|-----------|------|-------------|
| `max_cost` | float | Max cost per trade (USDC) |
| `entry_prices` | string | Comma-separated entry price caps |
| `late_window_sec` | float | Only trade in last N seconds of the 5m window |
| `min_spread` | float | Minimum \|Up_ask - Down_ask\| to trigger (e.g. 0.20) |

---

## Usage Examples

```bash
# 1. Standard backtest with current config
curl "http://localhost:8686/api/backtest/data"

# 2. Filter by date range
curl "http://localhost:8686/api/backtest/data?from=2025-03-01&to=2025-03-03"

# 3. Override params (temporarily increase vol_sigma)
curl "http://localhost:8686/api/backtest/data?params=vol_sigma=2.5,min_threshold=12"

# 4. Sweep to find optimal entry_price
curl "http://localhost:8686/api/backtest/data?sweep=ep=0.50:0.95:0.05"

# 5. 2D sweep + 70/30 split validation
curl "http://localhost:8686/api/backtest/data?sweep=ep=0.80:0.95:0.05,vs=1.0:2.5:0.5&split=0.7"

# 6. Walk-forward rolling validation
curl "http://localhost:8686/api/backtest/data?sweep=ep=0.80:0.95:0.05&wf=200:100:50"

# 7. Spread strategy sweep
curl "http://localhost:8686/api/backtest/data?sweep=ms=0.10:0.40:0.05,ep=0.50:0.95:0.05"
```

---

## Backtest Mechanics

### Data Source

- Market snapshots sampled every **1 second** from Polymarket orderbook via WebSocket
- Each sample records: `YesAsk`, `YesBid`, `NoAsk`, `NoBid`, `BTCPrice`, `ElapsedMs`, `RemainingMs`
- Stored in SQLite database (`windows` + `samples` tables)

### Trade Simulation

| Action | Price Used | Formula |
|--------|-----------|---------|
| Buy (Up) | `YesAsk` (best ask) | `fillPrice = mkt.BestAsk` |
| Buy (Down) | `NoAsk` (best ask) | `fillPrice = mkt.NoBestAsk` |
| Early exit (stop-loss) | `YesBid` or `NoBid` | `PnL = (sellPrice - entryPrice) * shares` |
| Window resolution (win) | Settlement at $1.00 | `PnL = (1.0 - buyPrice) * size` |
| Window resolution (loss) | Settlement at $0.00 | `PnL = -buyPrice * size` |

### Entry Conditions

All three modes (Late Sniper, Mean Reversion, Trend Following) share the same fill logic:

1. `ask > 0` — valid quote exists
2. `ask <= EntryPrice` — within limit order ceiling
3. `fillPrice = ask` — fill at the ask price (no slippage)
4. `size = MaxCost / fillPrice` — compute shares from cost

### Limitations

- **No slippage**: fills at exact `BestAsk` — real orders may get worse prices
- **No depth**: only checks best ask, does not consider order book depth vs trade size
- **No latency**: instant execution — real orders have network delay between signal and fill
- **No fees**: PnL does not deduct Polymarket trading fees (though `FeeRateBps` is carried in signals)

### Caching

- Results are cached by a key composed of: `windowCount|sweepSpec|split|wfSpec|paramsSpec|from|to`
- Cache is automatically invalidated when a new window is resolved
- Windows themselves are cached in memory with incremental loading from DB

### Concurrency

- Sweep combinations run concurrently with a semaphore limited to `runtime.NumCPU()`
- Multiple `entry_prices` in `RunAll()` also run concurrently (semaphore limit: CPU count)
