package config

import (
	"fmt"

	"github.com/spf13/viper"
)

const (
	configKeyManagementPort = "management.port"
	DefaultManagementPort   = 9081
	EnvManagementPort       = "MANAGEMENT__PORT"
)

// ManagementConfig описывает технический порт (Kubernetes-имя `metrics`):
// health и metrics отделены от бизнес-порта.
type ManagementConfig struct {
	Port int `mapstructure:"port"`
}

func configureManagement(loader *viper.Viper) error {
	loader.SetDefault(configKeyManagementPort, DefaultManagementPort)
	if err := loader.BindEnv(configKeyManagementPort, EnvManagementPort); err != nil {
		return fmt.Errorf("bind %s: %w", EnvManagementPort, err)
	}
	return nil
}

func (cfg ManagementConfig) Validate() error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("management port must be between 1 and 65535, got %d", cfg.Port)
	}
	return nil
}
