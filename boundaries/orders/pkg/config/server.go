package config

import "fmt"

// DefaultPort — бизнес-порт (spec.server.port).
const DefaultPort = 8081

type ServerConfig struct {
	Port int `yaml:"port"`
}

func (cfg ServerConfig) Validate() error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("spec.server.port must be between 1 and 65535, got %d", cfg.Port)
	}
	return nil
}
