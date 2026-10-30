package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

const (
	configKeySchemaRegistryURL     = "schema_registry.url"
	configKeySchemaRegistryTimeout = "schema_registry.timeout"

	EnvSchemaRegistryURL     = "SCHEMA_REGISTRY__URL"
	EnvSchemaRegistryTimeout = "SCHEMA_REGISTRY__TIMEOUT"

	DefaultSchemaRegistryTimeout = 5 * time.Second
)

// SchemaRegistryConfig locates the Schema Registry of Integration Events
// (FR-715). orders-consumer refuses to start without a URL; processes that
// do not read events leave it empty.
type SchemaRegistryConfig struct {
	URL     string        `mapstructure:"url"`
	Timeout time.Duration `mapstructure:"timeout"`
}

func configureSchemaRegistry(loader *viper.Viper) error {
	loader.SetDefault(configKeySchemaRegistryURL, "")
	loader.SetDefault(configKeySchemaRegistryTimeout, DefaultSchemaRegistryTimeout)
	for key, env := range map[string]string{
		configKeySchemaRegistryURL:     EnvSchemaRegistryURL,
		configKeySchemaRegistryTimeout: EnvSchemaRegistryTimeout,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

func (cfg SchemaRegistryConfig) Validate() error {
	if cfg.Timeout <= 0 {
		return fmt.Errorf("schema registry timeout must be positive, got %s", cfg.Timeout)
	}
	return nil
}
