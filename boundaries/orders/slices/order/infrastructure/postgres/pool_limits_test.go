package postgres

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
)

func TestApplyPoolLimitsCopiesConfiguredLimits(t *testing.T) {
	poolConfig, err := pgxpool.ParseConfig("postgres://orders@localhost:5432/orders")
	require.NoError(t, err)

	applyPoolLimits(poolConfig, config.PostgresConfig{
		MaxConns:              3,
		MinConns:              1,
		MaxConnLifetime:       30 * time.Minute,
		MaxConnLifetimeJitter: 5 * time.Minute,
		MaxConnIdleTime:       5 * time.Minute,
		ConnectTimeout:        5 * time.Second,
		HealthCheckPeriod:     time.Minute,
	})

	assert.Equal(t, int32(3), poolConfig.MaxConns)
	assert.Equal(t, int32(1), poolConfig.MinConns)
	assert.Equal(t, 30*time.Minute, poolConfig.MaxConnLifetime)
	assert.Equal(t, 5*time.Minute, poolConfig.MaxConnLifetimeJitter)
	assert.Equal(t, 5*time.Minute, poolConfig.MaxConnIdleTime)
	assert.Equal(t, time.Minute, poolConfig.HealthCheckPeriod)
	assert.Equal(t, 5*time.Second, poolConfig.ConnConfig.ConnectTimeout)
}

func TestApplyPoolLimitsKeepsDriverDefaultsForZeroDurations(t *testing.T) {
	poolConfig, err := pgxpool.ParseConfig("postgres://orders@localhost:5432/orders")
	require.NoError(t, err)
	lifetime, healthCheck := poolConfig.MaxConnLifetime, poolConfig.HealthCheckPeriod

	applyPoolLimits(poolConfig, config.PostgresConfig{MaxConns: 2})

	assert.Equal(t, int32(2), poolConfig.MaxConns)
	assert.Equal(t, lifetime, poolConfig.MaxConnLifetime)
	assert.Equal(t, healthCheck, poolConfig.HealthCheckPeriod)
	assert.Positive(t, poolConfig.HealthCheckPeriod)
}
