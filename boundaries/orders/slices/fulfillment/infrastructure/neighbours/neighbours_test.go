package neighbours_test

import (
	"context"
	"errors"
	ports "nomnom/orders/slices/fulfillment/applications/neighbours"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktemporal "go.temporal.io/sdk/temporal"

	"nomnom/orders/slices/fulfillment/infrastructure/neighbours"
	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

// AC-452: in the unavailable mode every Activity fails with a retryable error.
func TestUnavailableActivitiesStayRetryable(t *testing.T) {
	store := memory.NewStore()
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed))))
	a := &temporal.Activities{
		Orders:      store,
		Payments:    neighbours.Unavailable{},
		Restaurants: neighbours.Unavailable{},
		Delivery:    neighbours.Unavailable{},
	}
	step := temporal.StepInput{OrderID: "order-1", IdempotencyKey: "order:order-1:step"}
	ctx := context.Background()

	for name, call := range map[string]func() error{
		"authorize":       func() error { _, err := a.AuthorizePayment(ctx, step); return err },
		"capture":         func() error { _, err := a.CapturePayment(ctx, step); return err },
		"cancel auth":     func() error { return a.CancelPaymentAuthorization(ctx, step) },
		"refund":          func() error { return a.RefundPayment(ctx, step) },
		"request order":   func() error { return a.RequestRestaurantOrder(ctx, step) },
		"cancel order":    func() error { return a.CancelRestaurantOrder(ctx, step) },
		"create delivery": func() error { return a.CreateDelivery(ctx, step) },
		"replan delivery": func() error { return a.ReplanDelivery(ctx, step) },
		"cancel delivery": func() error { return a.CancelDelivery(ctx, step) },
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.ErrorIs(t, err, neighbours.ErrNotImplemented)
			var application *sdktemporal.ApplicationError
			assert.False(t, errors.As(err, &application), "must stay retryable")
		})
	}
}

func TestSimulatedApprovesEveryCall(t *testing.T) {
	ctx := context.Background()
	s := neighbours.Simulated{}

	authorized, err := s.Authorize(ctx, ports.PaymentRequest{})
	require.NoError(t, err)
	captured, err := s.Capture(ctx, ports.PaymentRequest{})
	require.NoError(t, err)

	assert.True(t, authorized.Approved)
	assert.True(t, captured.Approved)
	require.NoError(t, s.CancelAuthorization(ctx, ports.PaymentRequest{}))
	require.NoError(t, s.Refund(ctx, ports.PaymentRequest{}))
	require.NoError(t, s.RequestOrder(ctx, ports.RestaurantOrderRequest{}))
	require.NoError(t, s.CancelOrder(ctx, ports.RestaurantOrderRequest{}))
	require.NoError(t, s.Create(ctx, ports.DeliveryRequest{}))
	require.NoError(t, s.Replan(ctx, ports.DeliveryRequest{}))
	require.NoError(t, s.Cancel(ctx, ports.DeliveryRequest{}))
}
