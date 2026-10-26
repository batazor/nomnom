package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	EnvSimulatorInterval   = "SIMULATOR__INTERVAL"
	EnvSimulatorMinDelay   = "SIMULATOR__MIN_DELAY"
	EnvSimulatorMaxDelay   = "SIMULATOR__MAX_DELAY"
	EnvSimulatorCustomerID = "SIMULATOR__CUSTOMER_ID"
	EnvSimulatorMarketIDs  = "SIMULATOR__MARKET_IDS"
)

// simulatorMarketPattern is the form of market_id (ADR 41).
var simulatorMarketPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// SimulatorConfig drives the demo customer Simulator of cmd/orders (FR-605,
// FR-606): every Interval it checks whether a simulated action is due; each
// action waits a random delay in [MinDelay, MaxDelay]. CustomerID is the user
// the Simulator places and cancels Orders as. MarketIDs is the comma-separated
// list of Markets (ADR 41) whose open restaurants the Simulator orders from:
// ListRestaurants is per Market, and the Order takes the Market of the chosen
// Restaurant. No value has a default.
type SimulatorConfig struct {
	Interval   time.Duration `mapstructure:"interval"`
	MinDelay   time.Duration `mapstructure:"min_delay"`
	MaxDelay   time.Duration `mapstructure:"max_delay"`
	CustomerID string        `mapstructure:"customer_id"`
	MarketIDs  string        `mapstructure:"market_ids"`
}

// Markets is MarketIDs split by comma; Validate rejects an empty or malformed entry.
func (cfg SimulatorConfig) Markets() []string {
	if strings.TrimSpace(cfg.MarketIDs) == "" {
		return nil
	}
	markets := strings.Split(cfg.MarketIDs, ",")
	for i, market := range markets {
		markets[i] = strings.TrimSpace(market)
	}
	return markets
}

func configureSimulator(loader *viper.Viper) error {
	for key, env := range map[string]string{
		"simulator.interval":    EnvSimulatorInterval,
		"simulator.min_delay":   EnvSimulatorMinDelay,
		"simulator.max_delay":   EnvSimulatorMaxDelay,
		"simulator.customer_id": EnvSimulatorCustomerID,
		"simulator.market_ids":  EnvSimulatorMarketIDs,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

// Validate is called by the process that runs the Simulator (cmd/orders): an
// empty or invalid value stops it at startup (AC-765).
func (cfg SimulatorConfig) Validate() error {
	var errs []error
	if cfg.Interval <= 0 {
		errs = append(errs, fmt.Errorf("%s must be positive, got %s", EnvSimulatorInterval, cfg.Interval))
	}
	if cfg.MinDelay <= 0 {
		errs = append(errs, fmt.Errorf("%s must be positive, got %s", EnvSimulatorMinDelay, cfg.MinDelay))
	}
	if cfg.MaxDelay <= 0 {
		errs = append(errs, fmt.Errorf("%s must be positive, got %s", EnvSimulatorMaxDelay, cfg.MaxDelay))
	}
	if cfg.MinDelay > cfg.MaxDelay {
		errs = append(errs, fmt.Errorf("%s %s exceeds %s %s", EnvSimulatorMinDelay, cfg.MinDelay, EnvSimulatorMaxDelay, cfg.MaxDelay))
	}
	if strings.TrimSpace(cfg.CustomerID) == "" {
		errs = append(errs, fmt.Errorf("%s is required", EnvSimulatorCustomerID))
	}
	markets := cfg.Markets()
	if len(markets) == 0 {
		errs = append(errs, fmt.Errorf("%s is required", EnvSimulatorMarketIDs))
	}
	seen := make(map[string]struct{}, len(markets))
	for _, market := range markets {
		if !simulatorMarketPattern.MatchString(market) {
			errs = append(errs, fmt.Errorf("%s: market_id %q must match %s", EnvSimulatorMarketIDs, market, simulatorMarketPattern))
			continue
		}
		if _, duplicate := seen[market]; duplicate {
			errs = append(errs, fmt.Errorf("%s: market_id %q is repeated", EnvSimulatorMarketIDs, market))
		}
		seen[market] = struct{}{}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("simulator config: %w", err)
	}
	return nil
}
