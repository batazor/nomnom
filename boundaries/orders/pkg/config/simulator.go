package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	KeySimulatorInterval   = "spec.simulator.interval"
	KeySimulatorMinDelay   = "spec.simulator.minDelay"
	KeySimulatorMaxDelay   = "spec.simulator.maxDelay"
	KeySimulatorCustomerID = "spec.simulator.customerId"
	KeySimulatorMarketIDs  = "spec.simulator.marketIds"
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
	Interval   time.Duration `yaml:"interval"`
	MinDelay   time.Duration `yaml:"minDelay"`
	MaxDelay   time.Duration `yaml:"maxDelay"`
	CustomerID string        `yaml:"customerId"`
	MarketIDs  string        `yaml:"marketIds"`
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

// Validate is called by the process that runs the Simulator (cmd/orders): an
// empty or invalid value stops it at startup (AC-765).
func (cfg SimulatorConfig) Validate() error {
	var errs []error
	if cfg.Interval <= 0 {
		errs = append(errs, fmt.Errorf("%s must be positive, got %s", KeySimulatorInterval, cfg.Interval))
	}
	if cfg.MinDelay <= 0 {
		errs = append(errs, fmt.Errorf("%s must be positive, got %s", KeySimulatorMinDelay, cfg.MinDelay))
	}
	if cfg.MaxDelay <= 0 {
		errs = append(errs, fmt.Errorf("%s must be positive, got %s", KeySimulatorMaxDelay, cfg.MaxDelay))
	}
	if cfg.MinDelay > cfg.MaxDelay {
		errs = append(errs, fmt.Errorf("%s %s exceeds %s %s", KeySimulatorMinDelay, cfg.MinDelay, KeySimulatorMaxDelay, cfg.MaxDelay))
	}
	if strings.TrimSpace(cfg.CustomerID) == "" {
		errs = append(errs, fmt.Errorf("%s is required", KeySimulatorCustomerID))
	}
	markets := cfg.Markets()
	if len(markets) == 0 {
		errs = append(errs, fmt.Errorf("%s is required", KeySimulatorMarketIDs))
	}
	seen := make(map[string]struct{}, len(markets))
	for _, market := range markets {
		if !simulatorMarketPattern.MatchString(market) {
			errs = append(errs, fmt.Errorf("%s: market_id %q must match %s", KeySimulatorMarketIDs, market, simulatorMarketPattern))
			continue
		}
		if _, duplicate := seen[market]; duplicate {
			errs = append(errs, fmt.Errorf("%s: market_id %q is repeated", KeySimulatorMarketIDs, market))
		}
		seen[market] = struct{}{}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("simulator config: %w", err)
	}
	return nil
}
