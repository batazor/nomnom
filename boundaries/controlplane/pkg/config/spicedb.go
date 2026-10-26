package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	configKeySpiceDBEndpoint     = "spicedb.endpoint"
	configKeySpiceDBPresharedKey = "spicedb.preshared_key"
	configKeySpiceDBInsecure     = "spicedb.insecure"
	configKeySpiceDBTimeout      = "spicedb.timeout"

	EnvSpiceDBEndpoint     = "SPICEDB__ENDPOINT"
	EnvSpiceDBPresharedKey = "SPICEDB__PRESHARED_KEY"
	EnvSpiceDBInsecure     = "SPICEDB__INSECURE"
	EnvSpiceDBTimeout      = "SPICEDB__TIMEOUT"

	DefaultSpiceDBTimeout = 2 * time.Second
)

// SpiceDBConfig locates the authorization service (ADR 18). The control plane
// checks platform#manage_markets on every write, so it does not start without
// an endpoint and a preshared key.
type SpiceDBConfig struct {
	Endpoint     string        `mapstructure:"endpoint"`
	PresharedKey string        `mapstructure:"preshared_key"`
	Insecure     bool          `mapstructure:"insecure"`
	Timeout      time.Duration `mapstructure:"timeout"`
}

func configureSpiceDB(loader *viper.Viper) error {
	loader.SetDefault(configKeySpiceDBEndpoint, "")
	loader.SetDefault(configKeySpiceDBPresharedKey, "")
	loader.SetDefault(configKeySpiceDBInsecure, false)
	loader.SetDefault(configKeySpiceDBTimeout, DefaultSpiceDBTimeout)
	for key, env := range map[string]string{
		configKeySpiceDBEndpoint:     EnvSpiceDBEndpoint,
		configKeySpiceDBPresharedKey: EnvSpiceDBPresharedKey,
		configKeySpiceDBInsecure:     EnvSpiceDBInsecure,
		configKeySpiceDBTimeout:      EnvSpiceDBTimeout,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

func (cfg SpiceDBConfig) Validate() error {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return fmt.Errorf("%s is required: controlplane checks permissions in SpiceDB", EnvSpiceDBEndpoint)
	}
	if strings.TrimSpace(cfg.PresharedKey) == "" {
		return fmt.Errorf("%s is required", EnvSpiceDBPresharedKey)
	}
	if cfg.Timeout <= 0 {
		return fmt.Errorf("spicedb timeout must be positive, got %s", cfg.Timeout)
	}
	return nil
}
