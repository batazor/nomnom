package config

import (
	"fmt"
	"strings"
	"time"
)

const (
	KeySpiceDBEndpoint = "spec.spicedb.endpoint"
	// EnvSpiceDBPresharedKey — секрет: только env из Secret, поля в spec нет.
	EnvSpiceDBPresharedKey = "SPICEDB__PRESHARED_KEY"

	DefaultSpiceDBTimeout = 2 * time.Second
)

// SpiceDBConfig locates the authorization service (ADR 18). Processes that check
// permissions refuse to start without an endpoint.
type SpiceDBConfig struct {
	Endpoint string `yaml:"endpoint"`
	// PresharedKey comes from SPICEDB__PRESHARED_KEY; a key in the file is an unknown field.
	PresharedKey string        `yaml:"-"`
	Insecure     bool          `yaml:"insecure"`
	Timeout      time.Duration `yaml:"timeout"`
}

func (cfg SpiceDBConfig) Validate() error {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil
	}
	if strings.TrimSpace(cfg.PresharedKey) == "" {
		return fmt.Errorf("%s is required when %s is set", EnvSpiceDBPresharedKey, KeySpiceDBEndpoint)
	}
	if cfg.Timeout <= 0 {
		return fmt.Errorf("spec.spicedb.timeout must be positive, got %s", cfg.Timeout)
	}
	return nil
}
