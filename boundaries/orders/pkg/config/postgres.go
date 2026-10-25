package config

import (
	"fmt"

	"github.com/spf13/viper"
)

const (
	configKeyPostgresDSN      = "postgres.dsn"
	configKeyPostgresMaxConns = "postgres.max_conns"

	EnvPostgresDSN      = "POSTGRES__DSN"
	EnvPostgresMaxConns = "POSTGRES__MAX_CONNS"

	DefaultPostgresMaxConns = 10
)

// PostgresConfig locates the orders database (ADR 8). Every orders process
// requires the DSN; it comes from a Secret, never from a file in Git.
type PostgresConfig struct {
	DSN      string `mapstructure:"dsn"`
	MaxConns int32  `mapstructure:"max_conns"`
}

func configurePostgres(loader *viper.Viper) error {
	loader.SetDefault(configKeyPostgresDSN, "")
	loader.SetDefault(configKeyPostgresMaxConns, DefaultPostgresMaxConns)
	for key, env := range map[string]string{
		configKeyPostgresDSN:      EnvPostgresDSN,
		configKeyPostgresMaxConns: EnvPostgresMaxConns,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

func (cfg PostgresConfig) Validate() error {
	if cfg.MaxConns < 1 {
		return fmt.Errorf("postgres max_conns must be positive, got %d", cfg.MaxConns)
	}
	return nil
}
