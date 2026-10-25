package di

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/infrastructure/memory"
	"nomnom/orders/slices/order/infrastructure/neighbours"
	"nomnom/orders/slices/order/infrastructure/neighbours/grpcclient"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

var (
	_ Store = (*memory.Store)(nil)
	_ Store = (*postgres.Store)(nil)
)

func TestNewAuthorizerRequiresEndpoint(t *testing.T) {
	_, _, err := NewAuthorizer(config.SpiceDBConfig{}, nil)
	require.ErrorContains(t, err, config.EnvSpiceDBEndpoint)
}

func TestNewPostgresStoreRequiresDSN(t *testing.T) {
	_, _, err := NewPostgresStore(config.PostgresConfig{MaxConns: 1})
	require.ErrorContains(t, err, config.EnvPostgresDSN)
}

func TestNewNeighboursSelectsAdapters(t *testing.T) {
	neighboursFor := func(cfg config.FulfillmentConfig, gateway config.InternalGatewayConfig) Neighbours {
		t.Helper()
		selected, cleanup, err := NewNeighbours(cfg, gateway, config.IdentityConfig{})
		require.NoError(t, err)
		t.Cleanup(cleanup)
		return selected
	}
	unavailable := neighboursFor(config.FulfillmentConfig{Neighbours: config.NeighboursUnavailable}, config.InternalGatewayConfig{})
	simulated := neighboursFor(config.FulfillmentConfig{Neighbours: config.NeighboursSimulated}, config.InternalGatewayConfig{})
	viaGateway := neighboursFor(config.FulfillmentConfig{Neighbours: config.NeighboursGRPC}, config.InternalGatewayConfig{Address: "internal-gateway:8080"})

	assert.Equal(t, neighbours.Unavailable{}, unavailable.Payments)
	assert.Equal(t, neighbours.Simulated{}, simulated.Delivery)
	assert.IsType(t, &grpcclient.Payments{}, viaGateway.Payments)
	assert.IsType(t, &grpcclient.Delivery{}, viaGateway.Delivery)
}

func TestNewActivitiesInstrumentsHandlers(t *testing.T) {
	activities := NewActivities(memory.NewStore(), Neighbours{Payments: neighbours.Simulated{}}, observability.UseCases())

	assert.Equal(t, neighbours.Simulated{}, activities.Payments)
	assert.NotNil(t, activities.Complete)
	assert.NotNil(t, activities.Finalize)
}
