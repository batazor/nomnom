package config

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"github.com/spf13/viper"
)

const (
	configKeyCatalogMinimumTotal = "catalog.minimum_total"
	configKeyCatalogMaximumTotal = "catalog.maximum_total"
	configKeyCatalogTimeout      = "catalog.timeout"

	EnvCatalogMinimumTotal = "CATALOG__MINIMUM_TOTAL"
	EnvCatalogMaximumTotal = "CATALOG__MAXIMUM_TOTAL"
	EnvCatalogTimeout      = "CATALOG__TIMEOUT"

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
	MinimumTotal string        `mapstructure:"minimum_total"`
	MaximumTotal string        `mapstructure:"maximum_total"`
	Timeout      time.Duration `mapstructure:"timeout"`
}

func configureCatalog(loader *viper.Viper) error {
	loader.SetDefault(configKeyCatalogMinimumTotal, DefaultCatalogMinimumTotal)
	loader.SetDefault(configKeyCatalogMaximumTotal, DefaultCatalogMaximumTotal)
	loader.SetDefault(configKeyCatalogTimeout, DefaultCatalogTimeout)
	for key, env := range map[string]string{
		configKeyCatalogMinimumTotal: EnvCatalogMinimumTotal,
		configKeyCatalogMaximumTotal: EnvCatalogMaximumTotal,
		configKeyCatalogTimeout:      EnvCatalogTimeout,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

func (cfg CatalogConfig) Validate() error {
	minimum, err := decimal.NewFromString(cfg.MinimumTotal)
	if err != nil {
		return fmt.Errorf("catalog minimum_total must be a decimal, got %q", cfg.MinimumTotal)
	}
	maximum, err := decimal.NewFromString(cfg.MaximumTotal)
	if err != nil {
		return fmt.Errorf("catalog maximum_total must be a decimal, got %q", cfg.MaximumTotal)
	}
	if minimum.IsNegative() {
		return fmt.Errorf("catalog minimum_total must not be negative, got %s", cfg.MinimumTotal)
	}
	if maximum.LessThan(minimum) {
		return fmt.Errorf("catalog maximum_total %s must not be less than minimum_total %s", cfg.MaximumTotal, cfg.MinimumTotal)
	}
	if cfg.Timeout <= 0 {
		return fmt.Errorf("catalog timeout must be positive, got %s", cfg.Timeout)
	}
	return nil
}
