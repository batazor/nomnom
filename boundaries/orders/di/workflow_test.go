package di

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/applications/fulfillment"
)

// AC-514: the API client of Temporal is lazy: an unreachable Temporal does not
// fail the start, only the call that needs the workflow.
func TestFulfillmentWorkflowDoesNotDialAtStart(t *testing.T) {
	c, cleanup, err := NewTemporalClient(config.TemporalConfig{HostPort: "127.0.0.1:1", Namespace: "default"})
	require.NoError(t, err)
	t.Cleanup(cleanup)
	workflow := NewFulfillmentWorkflow(c, config.FulfillmentConfig{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert.Error(t, workflow.Start(ctx, fulfillment.StartRequest{OrderID: "order-1"}))
}
