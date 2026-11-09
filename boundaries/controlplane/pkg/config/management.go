package config

import "fmt"

// DefaultManagementPort — технический порт по умолчанию.
const DefaultManagementPort = 9086

// ManagementConfig описывает технический порт (Kubernetes-имя `metrics`): health.
type ManagementConfig struct {
	Port int `yaml:"port"`
}

func (cfg ManagementConfig) Validate() error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("spec.management.port must be between 1 and 65535, got %d", cfg.Port)
	}
	return nil
}
