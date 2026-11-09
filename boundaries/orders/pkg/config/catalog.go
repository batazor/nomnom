package config

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

const (
	// DefaultCatalogMinimumTotal and DefaultCatalogMaximumTotal are placeholders
	// until restaurants publishes order total limits or the product rule is agreed.
	DefaultCatalogMinimumTotal = "0"
	DefaultCatalogMaximumTotal = "100000"
	// DefaultCatalogTimeout bounds the reads of restaurants before the transaction.
	DefaultCatalogTimeout = 2 * time.Second
)

// CatalogConfig holds the order total limits of Place Order, as exact decimal
// amounts in the currency of the restaurant menu, and the deadline of the menu
// reads from restaurants.
type CatalogConfig struct {
	MinimumTotal string        `yaml:"minimumTotal"`
	MaximumTotal string        `yaml:"maximumTotal"`
	Timeout      time.Duration `yaml:"timeout"`
}

func (cfg CatalogConfig) Validate() error {
	minimum, err := decimal.NewFromString(cfg.MinimumTotal)
	if err != nil {
		return fmt.Errorf("spec.catalog.minimumTotal must be a decimal, got %q", cfg.MinimumTotal)
	}
	maximum, err := decimal.NewFromString(cfg.MaximumTotal)
	if err != nil {
		return fmt.Errorf("spec.catalog.maximumTotal must be a decimal, got %q", cfg.MaximumTotal)
	}
	if minimum.IsNegative() {
		return fmt.Errorf("spec.catalog.minimumTotal must not be negative, got %s", cfg.MinimumTotal)
	}
	if maximum.LessThan(minimum) {
		return fmt.Errorf("spec.catalog.maximumTotal %s must not be less than minimumTotal %s", cfg.MaximumTotal, cfg.MinimumTotal)
	}
	if cfg.Timeout <= 0 {
		return fmt.Errorf("spec.catalog.timeout must be positive, got %s", cfg.Timeout)
	}
	return nil
}
