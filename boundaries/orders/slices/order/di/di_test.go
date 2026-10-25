package di

import (
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/infrastructure/memory"
)

var (
	_ Store = (*memory.Store)(nil)
	_ Store = postgresStore{}
)

func TestNewAuthorizerRequiresEndpoint(t *testing.T) {
	_, _, err := NewAuthorizer(config.SpiceDBConfig{}, nil)
	require.ErrorContains(t, err, config.EnvSpiceDBEndpoint)
}

func TestNewPostgresStoreRequiresDSN(t *testing.T) {
	_, _, err := NewPostgresStore(config.PostgresConfig{MaxConns: 1})
	require.ErrorContains(t, err, config.EnvPostgresDSN)
}
