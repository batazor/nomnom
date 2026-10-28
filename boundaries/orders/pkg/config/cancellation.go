package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

const (
	configKeyCancellationWindow          = "cancellation.window"
	configKeyCancellationScheduledCutoff = "cancellation.scheduled_cutoff"
	// DefaultCancellationWindow is a placeholder until the product rule is agreed.
	DefaultCancellationWindow = 5 * time.Minute
	EnvCancellationWindow     = "CANCELLATION__WINDOW"
	// EnvCancellationScheduledCutoff has no default (FR-462): every process
	// refuses to start without it.
	EnvCancellationScheduledCutoff = "CANCELLATION__SCHEDULED_CUTOFF"
)

// CancellationConfig holds the cancellation policy: the window opens when the
// Order is placed and lasts Window; for a Scheduled Order it closes
// ScheduledCutoff before the Delivery Window starts.
type CancellationConfig struct {
	Window          time.Duration `mapstructure:"window"`
	ScheduledCutoff time.Duration `mapstructure:"scheduled_cutoff"`
}

func configureCancellation(loader *viper.Viper) error {
	loader.SetDefault(configKeyCancellationWindow, DefaultCancellationWindow)
	for key, env := range map[string]string{
		configKeyCancellationWindow:          EnvCancellationWindow,
		configKeyCancellationScheduledCutoff: EnvCancellationScheduledCutoff,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

func (cfg CancellationConfig) Validate() error {
	if cfg.Window <= 0 {
		return fmt.Errorf("cancellation window must be positive, got %s", cfg.Window)
	}
	if cfg.ScheduledCutoff <= 0 {
		return fmt.Errorf("%s must be positive, got %s", EnvCancellationScheduledCutoff, cfg.ScheduledCutoff)
	}
	return nil
}
