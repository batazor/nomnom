// Package config loads the configuration of the controlplane process from the
// environment (and CONFIG_FILE for local runs). Required settings without a
// value stop the start: the process never runs with part of its operations off.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Management ManagementConfig `mapstructure:"management"`
	Shutdown   ShutdownConfig   `mapstructure:"shutdown"`
	Routes     RoutesConfig     `mapstructure:"routes"`
}

func Load() (Config, error) {
	loader := viper.New()
	loader.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))

	for name, configure := range map[string]func(*viper.Viper) error{
		"management": configureManagement,
		"shutdown":   configureShutdown,
		"routes":     configureRoutes,
	} {
		if err := configure(loader); err != nil {
			return Config{}, fmt.Errorf("configure %s: %w", name, err)
		}
	}

	if path := os.Getenv("CONFIG_FILE"); path != "" {
		loader.SetConfigFile(path)
		if err := loader.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
	}

	var cfg Config
	if err := loader.UnmarshalExact(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks every section; the first invalid one stops the start.
func (cfg Config) Validate() error {
	for _, validate := range []func() error{cfg.Management.Validate, cfg.Shutdown.Validate, cfg.Routes.Validate} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}
