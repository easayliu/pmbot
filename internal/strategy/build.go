package strategy

import (
	"fmt"
	"strconv"
	"time"

	"github.com/easay/pmbot/internal/config"
)

// BuildFromConfig creates a Strategy from configuration.
func BuildFromConfig(cfg config.StrategyConfig) (Strategy, error) {
	switch cfg.Name {
	case "btc_updown":
		parseFloat := func(key string) (float64, error) {
			v, ok := cfg.Params[key]
			if !ok || v == "" {
				return 0, nil
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, fmt.Errorf("parse %s: %w", key, err)
			}
			return f, nil
		}

		maxCost, err := parseFloat("max_cost")
		if err != nil {
			return nil, err
		}
		entryPrice, err := parseFloat("entry_price")
		if err != nil {
			return nil, err
		}
		trendThreshold, err := parseFloat("trend_threshold")
		if err != nil {
			return nil, err
		}
		minElapsedSec, err := parseFloat("min_elapsed_sec")
		if err != nil {
			return nil, err
		}
		volSigma, err := parseFloat("vol_sigma")
		if err != nil {
			return nil, err
		}
		minThreshold, err := parseFloat("min_threshold")
		if err != nil {
			return nil, err
		}
		accelDecayVol, err := parseFloat("accel_decay_vol")
		if err != nil {
			return nil, err
		}
		minElapsedFloorSec, err := parseFloat("min_elapsed_floor_sec")
		if err != nil {
			return nil, err
		}
		elapsedPriceRef, err := parseFloat("elapsed_price_ref")
		if err != nil {
			return nil, err
		}
		trendConfirm := cfg.Params["trend_confirm"]
		trendDiscount, err := parseFloat("trend_discount")
		if err != nil {
			return nil, err
		}

		// Late-window sniper mode parameters.
		lateWindowSec, err := parseFloat("late_window_sec")
		if err != nil {
			return nil, err
		}
		lateWindowThresholdMul, err := parseFloat("late_window_threshold_mul")
		if err != nil {
			return nil, err
		}

		// Mean reversion mode parameters.
		meanRevSigma, err := parseFloat("mean_rev_sigma")
		if err != nil {
			return nil, err
		}
		meanRevMaxElapsedSec, err := parseFloat("mean_rev_max_elapsed_sec")
		if err != nil {
			return nil, err
		}

		// Streak reversal bias parameters.
		streakLenF, err := parseFloat("streak_len")
		if err != nil {
			return nil, err
		}
		streakDiscount, err := parseFloat("streak_discount")
		if err != nil {
			return nil, err
		}

		// Minimum signal strength filter.
		minSignalStrength, err := parseFloat("min_signal_strength")
		if err != nil {
			return nil, err
		}

		// Fair value gate.
		fairValueEdge, err := parseFloat("fair_value_edge")
		if err != nil {
			return nil, err
		}

		// Fair value stop-loss early exit.
		earlyExitStopFactor, err := parseFloat("early_exit_stop_factor")
		if err != nil {
			return nil, err
		}
		earlyExitMinHoldSec, err := parseFloat("early_exit_min_hold_sec")
		if err != nil {
			return nil, err
		}

		// Default TrendDiscount to 1.0 (no discount) when not configured.
		// TrendDiscount is a multiplier: 0.6 = 40% discount, 1.0 = disabled.
		// A zero value would collapse the threshold to 0, which is never intended.
		if trendDiscount <= 0 {
			trendDiscount = 1.0
		}

		return &BTCUpDownStrategy{
			MaxCost:         maxCost,
			EntryPrice:      entryPrice,
			TrendThreshold:  trendThreshold,
			MinElapsed:      time.Duration(minElapsedSec * float64(time.Second)),
			VolSigma:        volSigma,
			MinThreshold:    minThreshold,
			AccelDecayVol:   accelDecayVol,
			MinElapsedFloor: time.Duration(minElapsedFloorSec * float64(time.Second)),
			ElapsedPriceRef: elapsedPriceRef,
			TrendConfirm:    trendConfirm,
			TrendDiscount:   trendDiscount,
			// Late-window sniper.
			LateWindowSec:          lateWindowSec,
			LateWindowThresholdMul: lateWindowThresholdMul,
			// Mean reversion.
			MeanRevSigma:      meanRevSigma,
			MeanRevMaxElapsed: time.Duration(meanRevMaxElapsedSec * float64(time.Second)),
			// Streak reversal.
			StreakLen:      int(streakLenF),
			StreakDiscount: streakDiscount,
			// Signal strength filter.
			MinSignalStrength: minSignalStrength,
			// Fair value gate.
			FairValueEdge: fairValueEdge,
			// Fair value stop-loss early exit.
			EarlyExitStopFactor: earlyExitStopFactor,
			EarlyExitMinHoldSec: earlyExitMinHoldSec,
		}, nil

	default:
		return nil, fmt.Errorf("unknown strategy: %s", cfg.Name)
	}
}
