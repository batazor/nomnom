package config

import (
	"fmt"

	"github.com/spf13/viper"
)

const (
	configKeyPort = "server.port"
	DefaultPort   = 8086
	EnvPort       = "SERVER__PORT"
)

type ServerConfig struct {
	Port int `mapstructure:"port"`
}

func configureServer(loader *viper.Viper) error {
	loader.SetDefault(configKeyPort, DefaultPort)
	if err := loader.BindEnv(configKeyPort, EnvPort, "PORT"); err != nil {
		return fmt.Errorf("bind %s: %w", EnvPort, err)
	}
	return nil
}

func (cfg ServerConfig) Validate() error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("server port must be between 1 and 65535, got %d", cfg.Port)
	}
	return nil
}
