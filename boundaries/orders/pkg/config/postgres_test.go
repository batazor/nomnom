package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearPostgresEnvironment(t *testing.T) {
	t.Helper()
	clearConfigEnvironment(t)
	for _, name := range []string{
		EnvPostgresDSN, EnvPostgresMaxConns, EnvPostgresMinConns, EnvPostgresMaxConnLifetime,
		EnvPostgresMaxConnLifetimeJitter, EnvPostgresMaxConnIdleTime, EnvPostgresConnectTimeout,
		EnvPostgresHealthCheckPeriod,
	} {
		value, exists := os.LookupEnv(name)
		require.NoError(t, os.Unsetenv(name))
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(name, value)
			}
		})
	}
}

func TestLoadUsesPostgresPoolDefaults(t *testing.T) {
	clearPostgresEnvironment(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, PostgresConfig{
		MaxConns:              DefaultPostgresMaxConns,
		MinConns:              DefaultPostgresMinConns,
		MaxConnLifetime:       30 * time.Minute,
		MaxConnLifetimeJitter: 5 * time.Minute,
		MaxConnIdleTime:       5 * time.Minute,
		ConnectTimeout:        5 * time.Second,
		HealthCheckPeriod:     time.Minute,
	}, cfg.Postgres)
}

func TestLoadReadsPostgresPoolFromEnvironment(t *testing.T) {
	clearPostgresEnvironment(t)
	t.Setenv(EnvPostgresDSN, "postgres://orders@db/orders")
	t.Setenv(EnvPostgresMaxConns, "3")
	t.Setenv(EnvPostgresMinConns, "1")
	t.Setenv(EnvPostgresMaxConnLifetime, "20m")
	t.Setenv(EnvPostgresMaxConnLifetimeJitter, "0s")
	t.Setenv(EnvPostgresMaxConnIdleTime, "90s")
	t.Setenv(EnvPostgresConnectTimeout, "2s")
	t.Setenv(EnvPostgresHealthCheckPeriod, "30s")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, PostgresConfig{
		DSN:                   "postgres://orders@db/orders",
		MaxConns:              3,
		MinConns:              1,
		MaxConnLifetime:       20 * time.Minute,
		MaxConnLifetimeJitter: 0,
		MaxConnIdleTime:       90 * time.Second,
		ConnectTimeout:        2 * time.Second,
		HealthCheckPeriod:     30 * time.Second,
	}, cfg.Postgres)
}

func TestLoadRejectsInvalidPostgresPool(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"zero max conns":       {EnvPostgresMaxConns: "0"},
		"negative min conns":   {EnvPostgresMinConns: "-1"},
		"min conns above one":  {EnvPostgresMinConns: "2"},
		"zero lifetime":        {EnvPostgresMaxConnLifetime: "0s"},
		"negative jitter":      {EnvPostgresMaxConnLifetimeJitter: "-1s"},
		"zero idle time":       {EnvPostgresMaxConnIdleTime: "0s"},
		"zero connect timeout": {EnvPostgresConnectTimeout: "0s"},
		"zero health check":    {EnvPostgresHealthCheckPeriod: "0s"},
		"malformed duration":   {EnvPostgresConnectTimeout: "five"},
	} {
		t.Run(name, func(t *testing.T) {
			clearPostgresEnvironment(t)
			for key, value := range env {
				t.Setenv(key, value)
			}

			_, err := Load()
			require.Error(t, err)
		})
	}
}

func TestPostgresConfigAllowsMinConnsEqualToMaxConns(t *testing.T) {
	cfg := PostgresConfig{
		MaxConns: 1, MinConns: 1, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute,
		ConnectTimeout: time.Second, HealthCheckPeriod: time.Minute,
	}
	require.NoError(t, cfg.Validate())
}
