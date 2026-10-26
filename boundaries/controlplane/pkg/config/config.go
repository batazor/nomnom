// Package config loads the configuration of the controlplane process from the
// environment (and CONFIG_FILE for local runs). Required settings without a
// value stop the start: the process never runs with part of its operations off.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config/database"
)

type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	Management ManagementConfig `mapstructure:"management"`
	Shutdown   ShutdownConfig   `mapstructure:"shutdown"`
	Identity   IdentityConfig   `mapstructure:"identity"`
	SpiceDB    SpiceDBConfig    `mapstructure:"spicedb"`
	Database   database.Config  `mapstructure:"postgres"`
}

func Load() (Config, error) {
	loader := viper.New()
	loader.SetEnvKeyReplacer(strings.NewReplacer(".", "__"))

	for name, configure := range map[string]func(*viper.Viper) error{
		"server":     configureServer,
		"management": configureManagement,
		"shutdown":   configureShutdown,
		"identity":   configureIdentity,
		"spicedb":    configureSpiceDB,
		"database":   database.Configure,
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
	for _, validate := range []func() error{
		cfg.Server.Validate, cfg.Management.Validate, cfg.Shutdown.Validate,
		cfg.Identity.Validate, cfg.SpiceDB.Validate, cfg.Database.Validate,
	} {
		if err := validate(); err != nil {
			return err
		}
	}
	if cfg.Management.Port == cfg.Server.Port {
		return fmt.Errorf("management port must differ from server port %d", cfg.Server.Port)
	}
	return nil
}
