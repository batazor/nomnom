package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

const (
	configKeyCancellationWindow = "cancellation.window"
	// DefaultCancellationWindow is a placeholder until the product rule is agreed.
	DefaultCancellationWindow = 5 * time.Minute
	EnvCancellationWindow     = "CANCELLATION__WINDOW"
)

// CancellationConfig holds the cancellation policy: the window opens when the
// Order is placed and lasts Window.
type CancellationConfig struct {
	Window time.Duration `mapstructure:"window"`
}

func configureCancellation(loader *viper.Viper) error {
	loader.SetDefault(configKeyCancellationWindow, DefaultCancellationWindow)
	if err := loader.BindEnv(configKeyCancellationWindow, EnvCancellationWindow); err != nil {
		return fmt.Errorf("bind %s: %w", EnvCancellationWindow, err)
	}
	return nil
}

func (cfg CancellationConfig) Validate() error {
	if cfg.Window <= 0 {
		return fmt.Errorf("cancellation window must be positive, got %s", cfg.Window)
	}
	return nil
}
