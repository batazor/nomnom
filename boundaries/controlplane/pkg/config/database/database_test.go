package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func valid() Config {
	return Config{
		DSN: "postgres://controlplane@db/controlplane", MaxConns: 1, MinConns: 1, MaxConnLifetime: time.Minute,
		MaxConnIdleTime: time.Minute, ConnectTimeout: time.Second, HealthCheckPeriod: time.Minute,
	}
}

func TestConfigAllowsMinConnsEqualToMaxConns(t *testing.T) {
	require.NoError(t, valid().Validate())
}

func TestConfigRequiresDSN(t *testing.T) {
	cfg := valid()
	cfg.DSN = ""
	require.ErrorContains(t, cfg.Validate(), EnvPostgresDSN)
}
