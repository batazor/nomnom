package di

import (
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
)

func TestNewStoreRequiresDSN(t *testing.T) {
	_, _, err := NewStore(config.PostgresConfig{MaxConns: 1})
	require.ErrorContains(t, err, config.EnvPostgresDSN)
}
