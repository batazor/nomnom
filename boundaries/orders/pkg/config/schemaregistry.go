package config

import (
	"fmt"
	"time"
)

const (
	KeySchemaRegistryURL         = "spec.schemaRegistry.url"
	DefaultSchemaRegistryTimeout = 5 * time.Second
)

// SchemaRegistryConfig locates the Schema Registry of Integration Events
// (FR-715). orders-consumer refuses to start without a URL; processes that
// do not read events leave it empty.
type SchemaRegistryConfig struct {
	URL     string        `yaml:"url"`
	Timeout time.Duration `yaml:"timeout"`
}

func (cfg SchemaRegistryConfig) Validate() error {
	if cfg.Timeout <= 0 {
		return fmt.Errorf("spec.schemaRegistry.timeout must be positive, got %s", cfg.Timeout)
	}
	return nil
}
