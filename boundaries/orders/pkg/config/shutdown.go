package config

import (
	"fmt"
	"time"
)

const (
	DefaultDrainTimeout   = 20 * time.Second
	DefaultReadinessDelay = 5 * time.Second
)

type ShutdownConfig struct {
	// ReadinessDelay — ожидание распространения not ready до остановки приёма запросов.
	ReadinessDelay time.Duration `yaml:"readinessDelay"`
	DrainTimeout   time.Duration `yaml:"drainTimeout"`
}

func (cfg ShutdownConfig) Validate() error {
	if cfg.DrainTimeout <= 0 {
		return fmt.Errorf("spec.shutdown.drainTimeout must be positive, got %s", cfg.DrainTimeout)
	}
	if cfg.ReadinessDelay < 0 {
		return fmt.Errorf("spec.shutdown.readinessDelay must not be negative, got %s", cfg.ReadinessDelay)
	}
	return nil
}
