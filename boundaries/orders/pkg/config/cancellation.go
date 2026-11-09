package config

import (
	"fmt"
	"time"
)

const (
	// DefaultCancellationWindow is a placeholder until the product rule is agreed.
	DefaultCancellationWindow = 5 * time.Minute
	// KeyCancellationScheduledCutoff has no default (FR-462): every process
	// refuses to start without it.
	KeyCancellationScheduledCutoff = "spec.cancellation.scheduledCutoff"
)

// CancellationConfig holds the cancellation policy: the window opens when the
// Order is placed and lasts Window; for a Scheduled Order it closes
// ScheduledCutoff before the Delivery Window starts.
type CancellationConfig struct {
	Window          time.Duration `yaml:"window"`
	ScheduledCutoff time.Duration `yaml:"scheduledCutoff"`
}

func (cfg CancellationConfig) Validate() error {
	if cfg.Window <= 0 {
		return fmt.Errorf("spec.cancellation.window must be positive, got %s", cfg.Window)
	}
	if cfg.ScheduledCutoff <= 0 {
		return fmt.Errorf("%s must be positive, got %s", KeyCancellationScheduledCutoff, cfg.ScheduledCutoff)
	}
	return nil
}
