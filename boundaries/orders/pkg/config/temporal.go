package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	configKeyTemporalHostPort                 = "temporal.host_port"
	configKeyTemporalNamespace                = "temporal.namespace"
	configKeyTemporalConnectTimeout           = "temporal.connect_timeout"
	configKeyTemporalRegisterSearchAttributes = "temporal.register_search_attributes"

	EnvTemporalHostPort                 = "TEMPORAL__HOST_PORT"
	EnvTemporalNamespace                = "TEMPORAL__NAMESPACE"
	EnvTemporalConnectTimeout           = "TEMPORAL__CONNECT_TIMEOUT"
	EnvTemporalRegisterSearchAttributes = "TEMPORAL__REGISTER_SEARCH_ATTRIBUTES"

	DefaultTemporalHostPort       = "localhost:7233"
	DefaultTemporalNamespace      = "default"
	DefaultTemporalConnectTimeout = 10 * time.Second
)

// TemporalConfig is used by the worker process; the API process loads it but
// does not connect to Temporal. The task queue is fixed by ADR 5.
type TemporalConfig struct {
	HostPort       string        `mapstructure:"host_port"`
	Namespace      string        `mapstructure:"namespace"`
	ConnectTimeout time.Duration `mapstructure:"connect_timeout"`
	// RegisterSearchAttributes allows the worker to create missing custom
	// Search Attributes; it needs operator permissions in the namespace.
	RegisterSearchAttributes bool `mapstructure:"register_search_attributes"`
}

func configureTemporal(loader *viper.Viper) error {
	loader.SetDefault(configKeyTemporalHostPort, DefaultTemporalHostPort)
	loader.SetDefault(configKeyTemporalNamespace, DefaultTemporalNamespace)
	loader.SetDefault(configKeyTemporalConnectTimeout, DefaultTemporalConnectTimeout)
	loader.SetDefault(configKeyTemporalRegisterSearchAttributes, false)

	for key, env := range map[string]string{
		configKeyTemporalHostPort:                 EnvTemporalHostPort,
		configKeyTemporalNamespace:                EnvTemporalNamespace,
		configKeyTemporalConnectTimeout:           EnvTemporalConnectTimeout,
		configKeyTemporalRegisterSearchAttributes: EnvTemporalRegisterSearchAttributes,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

func (cfg TemporalConfig) Validate() error {
	if strings.TrimSpace(cfg.HostPort) == "" {
		return fmt.Errorf("temporal host_port must not be empty")
	}
	if strings.TrimSpace(cfg.Namespace) == "" {
		return fmt.Errorf("temporal namespace must not be empty")
	}
	if cfg.ConnectTimeout <= 0 {
		return fmt.Errorf("temporal connect timeout must be positive, got %s", cfg.ConnectTimeout)
	}
	return nil
}
