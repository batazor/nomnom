package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// ReconcilerConfig drives the reconciliation of order#customer relationships
// (docs/security.md). Grace must exceed the longest placement transaction.
type ReconcilerConfig struct {
	Interval time.Duration `mapstructure:"interval"`
	Grace    time.Duration `mapstructure:"grace"`
	PageSize int           `mapstructure:"page_size"`
}

func configureReconciler(loader *viper.Viper) error {
	for _, setting := range []struct {
		key, env string
		value    any
	}{
		{"reconciler.interval", "RECONCILER__INTERVAL", 5 * time.Minute},
		{"reconciler.grace", "RECONCILER__GRACE", 10 * time.Minute},
		{"reconciler.page_size", "RECONCILER__PAGE_SIZE", 500},
	} {
		loader.SetDefault(setting.key, setting.value)
		if err := loader.BindEnv(setting.key, setting.env); err != nil {
			return fmt.Errorf("bind %s: %w", setting.env, err)
		}
	}
	return nil
}

func (cfg ReconcilerConfig) Validate() error {
	if cfg.Interval <= 0 || cfg.Grace <= 0 {
		return fmt.Errorf("reconciler interval and grace must be positive, got %s and %s", cfg.Interval, cfg.Grace)
	}
	if cfg.PageSize < 1 || cfg.PageSize > 1000 {
		return fmt.Errorf("reconciler page_size must be between 1 and 1000, got %d", cfg.PageSize)
	}
	return nil
}
