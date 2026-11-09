package database

import (
	"fmt"
	"strings"
	"time"
)

const (
	// EnvPostgresDSN — секрет: только env из Secret, поля в spec нет.
	EnvPostgresDSN = "POSTGRES__DSN"

	// Defaults fit one API process behind a session-mode pooler; Helm sizes
	// MaxConns per process (worker, consumer).
	DefaultMaxConns              = 10
	DefaultMinConns              = 0
	DefaultMaxConnLifetime       = 30 * time.Minute
	DefaultMaxConnLifetimeJitter = 5 * time.Minute
	DefaultMaxConnIdleTime       = 5 * time.Minute
	DefaultConnectTimeout        = 5 * time.Second
	DefaultHealthCheckPeriod     = 1 * time.Minute

	// MaxMinConns keeps idle pooled connections from pinning server
	// connections of the session-mode pooler.
	MaxMinConns = 1
)

// Defaults — значения spec.postgres по умолчанию.
var Defaults = Config{
	MaxConns:              DefaultMaxConns,
	MinConns:              DefaultMinConns,
	MaxConnLifetime:       DefaultMaxConnLifetime,
	MaxConnLifetimeJitter: DefaultMaxConnLifetimeJitter,
	MaxConnIdleTime:       DefaultMaxConnIdleTime,
	ConnectTimeout:        DefaultConnectTimeout,
	HealthCheckPeriod:     DefaultHealthCheckPeriod,
}

// Config locates the orders database (ADR 8) and sizes the process
// pool (spec.postgres). Every orders process requires the DSN; it comes from
// POSTGRES__DSN of a Secret, never from the file: a dsn key in the file is an
// unknown field.
type Config struct {
	DSN                   string        `yaml:"-"`
	MaxConns              int32         `yaml:"maxConns"`
	MinConns              int32         `yaml:"minConns"`
	MaxConnLifetime       time.Duration `yaml:"maxConnLifetime"`
	MaxConnLifetimeJitter time.Duration `yaml:"maxConnLifetimeJitter"`
	MaxConnIdleTime       time.Duration `yaml:"maxConnIdleTime"`
	ConnectTimeout        time.Duration `yaml:"connectTimeout"`
	HealthCheckPeriod     time.Duration `yaml:"healthCheckPeriod"`
}

func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.DSN) == "" {
		return fmt.Errorf("%s is required: orders keeps its state in PostgreSQL", EnvPostgresDSN)
	}
	if cfg.MaxConns < 1 {
		return fmt.Errorf("spec.postgres.maxConns must be positive, got %d", cfg.MaxConns)
	}
	if cfg.MinConns < 0 || cfg.MinConns > MaxMinConns {
		return fmt.Errorf("spec.postgres.minConns must be between 0 and %d, got %d", MaxMinConns, cfg.MinConns)
	}
	if cfg.MinConns > cfg.MaxConns {
		return fmt.Errorf("spec.postgres.minConns %d must not exceed maxConns %d", cfg.MinConns, cfg.MaxConns)
	}
	for name, value := range map[string]time.Duration{
		"maxConnLifetime":   cfg.MaxConnLifetime,
		"maxConnIdleTime":   cfg.MaxConnIdleTime,
		"connectTimeout":    cfg.ConnectTimeout,
		"healthCheckPeriod": cfg.HealthCheckPeriod,
	} {
		if value <= 0 {
			return fmt.Errorf("spec.postgres.%s must be positive, got %s", name, value)
		}
	}
	if cfg.MaxConnLifetimeJitter < 0 {
		return fmt.Errorf("spec.postgres.maxConnLifetimeJitter must not be negative, got %s", cfg.MaxConnLifetimeJitter)
	}
	return nil
}
