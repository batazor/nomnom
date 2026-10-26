package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigAllowsMinConnsEqualToMaxConns(t *testing.T) {
	cfg := Config{
		MaxConns: 1, MinConns: 1, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute,
		ConnectTimeout: time.Second, HealthCheckPeriod: time.Minute,
	}
	require.NoError(t, cfg.Validate())
}
