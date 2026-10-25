package di

import (
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/infrastructure/memory"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/spicedb"
)

// The in-memory store of unit tests and the process store play every role.
var (
	_ PlacementStore         = (*memory.Store)(nil)
	_ ReadModels             = (*memory.Store)(nil)
	_ InboxStore             = (*memory.Store)(nil)
	_ spicedb.RevisionReader = (*memory.Store)(nil)
	_ PlacementStore         = (*ProcessStore)(nil)
	_ ReadModels             = (*ProcessStore)(nil)
	_ InboxStore             = (*ProcessStore)(nil)
	_ spicedb.RevisionReader = (*ProcessStore)(nil)
	_ observability.Outbox   = (*ProcessStore)(nil)
	_ application.UnitOfWork = (*ProcessStore)(nil)
)

func TestNewAuthorizerRequiresEndpoint(t *testing.T) {
	_, _, err := NewAuthorizer(config.SpiceDBConfig{}, nil)
	require.ErrorContains(t, err, config.EnvSpiceDBEndpoint)
}

func TestNewPostgresStoreRequiresDSN(t *testing.T) {
	_, _, err := NewPostgresStore(config.PostgresConfig{MaxConns: 1})
	require.ErrorContains(t, err, config.EnvPostgresDSN)
}
