package di

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/fulfillment/applications/orders"
	orderdi "nomnom/orders/slices/order/di"
)

// A positive or unknown outcome is not a final negative decision: it is refused
// before the use case, which the empty module would fail on.
func TestFailOrCancelRefusesOutcomeThatIsNotNegative(t *testing.T) {
	port := NewFulfillmentOrders(&orderdi.FulfillmentModule{})

	err := port.FailOrCancel(context.Background(), orders.FinalDecision{OrderID: "order-1", Outcome: "completed", DecidedAt: time.Now()})

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
	assert.Equal(t, usecase.KindInvalidInput, usecase.KindOf(err))
}
