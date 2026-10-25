package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

const (
	configKeyDrainTimeout = "shutdown.drain_timeout"
	DefaultDrainTimeout   = 20 * time.Second
	EnvDrainTimeout       = "SHUTDOWN__DRAIN_TIMEOUT"

	configKeyReadinessDelay = "shutdown.readiness_delay"
	DefaultReadinessDelay   = 5 * time.Second
	EnvReadinessDelay       = "SHUTDOWN__READINESS_DELAY"
)

type ShutdownConfig struct {
	// ReadinessDelay — ожидание распространения not ready до остановки приёма запросов.
	ReadinessDelay time.Duration `mapstructure:"readiness_delay"`
	DrainTimeout   time.Duration `mapstructure:"drain_timeout"`
}

func configureShutdown(loader *viper.Viper) error {
	loader.SetDefault(configKeyDrainTimeout, DefaultDrainTimeout)
	if err := loader.BindEnv(configKeyDrainTimeout, EnvDrainTimeout); err != nil {
		return fmt.Errorf("bind %s: %w", EnvDrainTimeout, err)
	}
	loader.SetDefault(configKeyReadinessDelay, DefaultReadinessDelay)
	if err := loader.BindEnv(configKeyReadinessDelay, EnvReadinessDelay); err != nil {
		return fmt.Errorf("bind %s: %w", EnvReadinessDelay, err)
	}
	return nil
}

func (cfg ShutdownConfig) Validate() error {
	if cfg.DrainTimeout <= 0 {
		return fmt.Errorf("shutdown drain timeout must be positive, got %s", cfg.DrainTimeout)
	}
	if cfg.ReadinessDelay < 0 {
		return fmt.Errorf("shutdown readiness delay must not be negative, got %s", cfg.ReadinessDelay)
	}
	return nil
}
