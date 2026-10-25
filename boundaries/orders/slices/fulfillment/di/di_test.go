package di

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/fulfillment/infrastructure/neighbours/grpcclient"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/postgres/postgrestest"
)

func TestNewNeighboursDialsTheGateway(t *testing.T) {
	selected, cleanup, err := NewNeighbours(config.InternalGatewayConfig{Address: "internal-gateway:8080"}, config.IdentityConfig{})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	assert.IsType(t, &grpcclient.Payments{}, selected.Payments)
	assert.IsType(t, &grpcclient.Delivery{}, selected.Delivery)
}

func TestNewActivitiesInstrumentsHandlers(t *testing.T) {
	payments := &grpcclient.Payments{}
	activities := NewActivities(postgrestest.Open(t), Neighbours{Payments: payments}, observability.UseCases())

	assert.Same(t, payments, activities.Payments)
	assert.NotNil(t, activities.Complete)
	assert.NotNil(t, activities.Finalize)
}
