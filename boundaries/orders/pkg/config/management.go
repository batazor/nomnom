package config

import "fmt"

// DefaultManagementPort — технический порт (spec.management.port).
const DefaultManagementPort = 9081

// ManagementConfig описывает технический порт (Kubernetes-имя `metrics`):
// health и metrics отделены от бизнес-порта.
type ManagementConfig struct {
	Port int `yaml:"port"`
}

func (cfg ManagementConfig) Validate() error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("spec.management.port must be between 1 and 65535, got %d", cfg.Port)
	}
	return nil
}
