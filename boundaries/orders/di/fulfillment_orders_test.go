package di

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/fulfillment/applications/orders"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order"
)

// AC-635: the table of the port decides for every usecase.ErrorKind.
func TestPortErrorTableCoversEveryKind(t *testing.T) {
	for _, kind := range usecase.ErrorKinds() {
		_, ok := portErrorByKind[kind]
		assert.True(t, ok, kind)
	}
}

func TestPortErrorTranslatesUseCaseErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"invalid input", usecase.InvalidInput("order_id", "is required"), usecase.ErrInvalidInput},
		{"missing order", order.ErrOrderNotFound, orders.ErrOrderNotFound},
		{"invalid transition", order.InvalidTransition{}, orders.ErrStepRefused},
		{"failed guard", order.GuardFailed{Reason: "completed_at_required"}, orders.ErrStepRefused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := portError(tt.err)

			require.ErrorIs(t, err, tt.want)
			assert.ErrorIs(t, err, tt.err, "the cause stays in the chain")
		})
	}
	assert.NoError(t, portError(nil))
}

// A kind without an error of the port stays as is: the Activity retries it.
func TestPortErrorKeepsRetryableErrors(t *testing.T) {
	for _, cause := range []error{usecase.ErrConcurrencyConflict, usecase.ErrDependencyUnavailable, errors.New("reset")} {
		assert.Equal(t, cause, portError(cause))
	}
}

// A positive or unknown outcome is not a final negative decision: it is refused
// before the use case, which the empty module would fail on.
func TestFailOrCancelRefusesOutcomeThatIsNotNegative(t *testing.T) {
	port := NewFulfillmentOrders(&orderdi.FulfillmentModule{})

	err := port.FailOrCancel(context.Background(), orders.FinalDecision{OrderID: "order-1", Outcome: "completed", DecidedAt: time.Now()})

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}
