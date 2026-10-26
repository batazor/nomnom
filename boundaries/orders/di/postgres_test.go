package di

import (
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
)

func TestNewPostgresStoreRequiresDSN(t *testing.T) {
	_, _, err := NewPostgresStore(config.PostgresConfig{MaxConns: 1})
	require.ErrorContains(t, err, config.EnvPostgresDSN)
}
