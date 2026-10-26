package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config/database"
)

func clearDatabaseEnvironment(t *testing.T) {
	t.Helper()
	clearConfigEnvironment(t)
	for _, name := range []string{
		database.EnvPostgresDSN, database.EnvPostgresMaxConns, database.EnvPostgresMinConns, database.EnvPostgresMaxConnLifetime,
		database.EnvPostgresMaxConnLifetimeJitter, database.EnvPostgresMaxConnIdleTime, database.EnvPostgresConnectTimeout,
		database.EnvPostgresHealthCheckPeriod,
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

func TestLoadUsesDatabasePoolDefaults(t *testing.T) {
	clearDatabaseEnvironment(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, database.Config{
		MaxConns:              database.DefaultMaxConns,
		MinConns:              database.DefaultMinConns,
		MaxConnLifetime:       30 * time.Minute,
		MaxConnLifetimeJitter: 5 * time.Minute,
		MaxConnIdleTime:       5 * time.Minute,
		ConnectTimeout:        5 * time.Second,
		HealthCheckPeriod:     time.Minute,
	}, cfg.Database)
}

func TestLoadReadsDatabasePoolFromEnvironment(t *testing.T) {
	clearDatabaseEnvironment(t)
	t.Setenv(database.EnvPostgresDSN, "postgres://orders@db/orders")
	t.Setenv(database.EnvPostgresMaxConns, "3")
	t.Setenv(database.EnvPostgresMinConns, "1")
	t.Setenv(database.EnvPostgresMaxConnLifetime, "20m")
	t.Setenv(database.EnvPostgresMaxConnLifetimeJitter, "0s")
	t.Setenv(database.EnvPostgresMaxConnIdleTime, "90s")
	t.Setenv(database.EnvPostgresConnectTimeout, "2s")
	t.Setenv(database.EnvPostgresHealthCheckPeriod, "30s")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, database.Config{
		DSN:                   "postgres://orders@db/orders",
		MaxConns:              3,
		MinConns:              1,
		MaxConnLifetime:       20 * time.Minute,
		MaxConnLifetimeJitter: 0,
		MaxConnIdleTime:       90 * time.Second,
		ConnectTimeout:        2 * time.Second,
		HealthCheckPeriod:     30 * time.Second,
	}, cfg.Database)
}

func TestLoadRejectsInvalidDatabasePool(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"zero max conns":       {database.EnvPostgresMaxConns: "0"},
		"negative min conns":   {database.EnvPostgresMinConns: "-1"},
		"min conns above one":  {database.EnvPostgresMinConns: "2"},
		"zero lifetime":        {database.EnvPostgresMaxConnLifetime: "0s"},
		"negative jitter":      {database.EnvPostgresMaxConnLifetimeJitter: "-1s"},
		"zero idle time":       {database.EnvPostgresMaxConnIdleTime: "0s"},
		"zero connect timeout": {database.EnvPostgresConnectTimeout: "0s"},
		"zero health check":    {database.EnvPostgresHealthCheckPeriod: "0s"},
		"malformed duration":   {database.EnvPostgresConnectTimeout: "five"},
	} {
		t.Run(name, func(t *testing.T) {
			clearDatabaseEnvironment(t)
			for key, value := range env {
				t.Setenv(key, value)
			}

			_, err := Load()
			require.Error(t, err)
		})
	}
}
