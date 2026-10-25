package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

const (
	configKeyPostgresDSN                   = "postgres.dsn"
	configKeyPostgresMaxConns              = "postgres.max_conns"
	configKeyPostgresMinConns              = "postgres.min_conns"
	configKeyPostgresMaxConnLifetime       = "postgres.max_conn_lifetime"
	configKeyPostgresMaxConnLifetimeJitter = "postgres.max_conn_lifetime_jitter"
	configKeyPostgresMaxConnIdleTime       = "postgres.max_conn_idle_time"
	configKeyPostgresConnectTimeout        = "postgres.connect_timeout"
	configKeyPostgresHealthCheckPeriod     = "postgres.health_check_period"

	EnvPostgresDSN                   = "POSTGRES__DSN"
	EnvPostgresMaxConns              = "POSTGRES__MAX_CONNS"
	EnvPostgresMinConns              = "POSTGRES__MIN_CONNS"
	EnvPostgresMaxConnLifetime       = "POSTGRES__MAX_CONN_LIFETIME"
	EnvPostgresMaxConnLifetimeJitter = "POSTGRES__MAX_CONN_LIFETIME_JITTER"
	EnvPostgresMaxConnIdleTime       = "POSTGRES__MAX_CONN_IDLE_TIME"
	EnvPostgresConnectTimeout        = "POSTGRES__CONNECT_TIMEOUT"
	EnvPostgresHealthCheckPeriod     = "POSTGRES__HEALTH_CHECK_PERIOD"

	// Defaults fit one API process behind a session-mode pooler; Helm sizes
	// MaxConns per process (worker, consumer, forwarder, reconciler).
	DefaultPostgresMaxConns              = 10
	DefaultPostgresMinConns              = 0
	DefaultPostgresMaxConnLifetime       = 30 * time.Minute
	DefaultPostgresMaxConnLifetimeJitter = 5 * time.Minute
	DefaultPostgresMaxConnIdleTime       = 5 * time.Minute
	DefaultPostgresConnectTimeout        = 5 * time.Second
	DefaultPostgresHealthCheckPeriod     = 1 * time.Minute

	// MaxPostgresMinConns keeps idle pooled connections from pinning server
	// connections of the session-mode pooler.
	MaxPostgresMinConns = 1
)

// PostgresConfig locates the orders database (ADR 8) and sizes the process
// pool. Every orders process requires the DSN; it comes from a Secret, never
// from a file in Git.
type PostgresConfig struct {
	DSN                   string        `mapstructure:"dsn"`
	MaxConns              int32         `mapstructure:"max_conns"`
	MinConns              int32         `mapstructure:"min_conns"`
	MaxConnLifetime       time.Duration `mapstructure:"max_conn_lifetime"`
	MaxConnLifetimeJitter time.Duration `mapstructure:"max_conn_lifetime_jitter"`
	MaxConnIdleTime       time.Duration `mapstructure:"max_conn_idle_time"`
	ConnectTimeout        time.Duration `mapstructure:"connect_timeout"`
	HealthCheckPeriod     time.Duration `mapstructure:"health_check_period"`
}

func configurePostgres(loader *viper.Viper) error {
	for key, value := range map[string]any{
		configKeyPostgresDSN:                   "",
		configKeyPostgresMaxConns:              DefaultPostgresMaxConns,
		configKeyPostgresMinConns:              DefaultPostgresMinConns,
		configKeyPostgresMaxConnLifetime:       DefaultPostgresMaxConnLifetime,
		configKeyPostgresMaxConnLifetimeJitter: DefaultPostgresMaxConnLifetimeJitter,
		configKeyPostgresMaxConnIdleTime:       DefaultPostgresMaxConnIdleTime,
		configKeyPostgresConnectTimeout:        DefaultPostgresConnectTimeout,
		configKeyPostgresHealthCheckPeriod:     DefaultPostgresHealthCheckPeriod,
	} {
		loader.SetDefault(key, value)
	}
	for key, env := range map[string]string{
		configKeyPostgresDSN:                   EnvPostgresDSN,
		configKeyPostgresMaxConns:              EnvPostgresMaxConns,
		configKeyPostgresMinConns:              EnvPostgresMinConns,
		configKeyPostgresMaxConnLifetime:       EnvPostgresMaxConnLifetime,
		configKeyPostgresMaxConnLifetimeJitter: EnvPostgresMaxConnLifetimeJitter,
		configKeyPostgresMaxConnIdleTime:       EnvPostgresMaxConnIdleTime,
		configKeyPostgresConnectTimeout:        EnvPostgresConnectTimeout,
		configKeyPostgresHealthCheckPeriod:     EnvPostgresHealthCheckPeriod,
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
	if cfg.MinConns < 0 || cfg.MinConns > MaxPostgresMinConns {
		return fmt.Errorf("postgres min_conns must be between 0 and %d, got %d", MaxPostgresMinConns, cfg.MinConns)
	}
	if cfg.MinConns > cfg.MaxConns {
		return fmt.Errorf("postgres min_conns %d must not exceed max_conns %d", cfg.MinConns, cfg.MaxConns)
	}
	for name, value := range map[string]time.Duration{
		"max_conn_lifetime":   cfg.MaxConnLifetime,
		"max_conn_idle_time":  cfg.MaxConnIdleTime,
		"connect_timeout":     cfg.ConnectTimeout,
		"health_check_period": cfg.HealthCheckPeriod,
	} {
		if value <= 0 {
			return fmt.Errorf("postgres %s must be positive, got %s", name, value)
		}
	}
	if cfg.MaxConnLifetimeJitter < 0 {
		return fmt.Errorf("postgres max_conn_lifetime_jitter must not be negative, got %s", cfg.MaxConnLifetimeJitter)
	}
	return nil
}
