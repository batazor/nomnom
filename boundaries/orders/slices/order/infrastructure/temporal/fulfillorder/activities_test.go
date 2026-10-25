package fulfillorder_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"

	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
	"nomnom/orders/slices/order/infrastructure/temporal/fulfillorder"
)

type fakeNeighbours struct {
	payments    []fulfillorder.PaymentRequest
	restaurants []fulfillorder.RestaurantOrderRequest
	deliveries  []fulfillorder.DeliveryRequest
	decision    fulfillorder.PaymentDecision
	err         error
}

func (f *fakeNeighbours) Authorize(_ context.Context, r fulfillorder.PaymentRequest) (fulfillorder.PaymentDecision, error) {
	f.payments = append(f.payments, r)
	return f.decision, f.err
}

func (f *fakeNeighbours) Capture(_ context.Context, r fulfillorder.PaymentRequest) (fulfillorder.PaymentDecision, error) {
	f.payments = append(f.payments, r)
	return f.decision, f.err
}

func (f *fakeNeighbours) CancelAuthorization(_ context.Context, r fulfillorder.PaymentRequest) error {
	f.payments = append(f.payments, r)
	return f.err
}

func (f *fakeNeighbours) Refund(_ context.Context, r fulfillorder.PaymentRequest) error {
	f.payments = append(f.payments, r)
	return f.err
}

func (f *fakeNeighbours) RequestOrder(_ context.Context, r fulfillorder.RestaurantOrderRequest) error {
	f.restaurants = append(f.restaurants, r)
	return f.err
}

func (f *fakeNeighbours) CancelOrder(_ context.Context, r fulfillorder.RestaurantOrderRequest) error {
	f.restaurants = append(f.restaurants, r)
	return f.err
}

func (f *fakeNeighbours) Create(_ context.Context, r fulfillorder.DeliveryRequest) error {
	f.deliveries = append(f.deliveries, r)
	return f.err
}

func (f *fakeNeighbours) Replan(_ context.Context, r fulfillorder.DeliveryRequest) error {
	f.deliveries = append(f.deliveries, r)
	return f.err
}

func (f *fakeNeighbours) Cancel(_ context.Context, r fulfillorder.DeliveryRequest) error {
	f.deliveries = append(f.deliveries, r)
	return f.err
}

func activities(t *testing.T, state order.State) (*fulfillorder.Activities, *fakeNeighbours, *memory.Store) {
	t.Helper()
	store := memory.NewStore()
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, orderID, ordertest.WithState(state))))
	neighbours := &fakeNeighbours{decision: fulfillorder.PaymentDecision{Approved: true}}
	return &fulfillorder.Activities{
		Orders:      store,
		Payments:    neighbours,
		Restaurants: neighbours,
		Delivery:    neighbours,
		Complete:    completeorder.NewHandler(store, store, store),
		Finalize:    failorcancelorder.NewHandler(store, store, store),
	}, neighbours, store
}

var step = fulfillorder.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:authorize_payment"}

func TestPaymentActivitiesUseOrderSnapshotAndStableKey(t *testing.T) {
	a, neighbours, _ := activities(t, order.StatePlaced)
	neighbours.decision = fulfillorder.PaymentDecision{Reason: "card_declined"}

	decision, err := a.AuthorizePayment(context.Background(), step)

	require.NoError(t, err, "a decline is a business result, not an error")
	assert.Equal(t, fulfillorder.PaymentDecision{Reason: "card_declined"}, decision)
	assert.Equal(t, []fulfillorder.PaymentRequest{{
		OrderID:        orderID,
		CustomerID:     "customer-1",
		Amount:         "20",
		Currency:       "EUR",
		IdempotencyKey: "order:order-1:authorize_payment",
	}}, neighbours.payments)
}

func TestRestaurantAndDeliveryActivitiesUseOrderSnapshot(t *testing.T) {
	a, neighbours, _ := activities(t, order.StatePlaced)
	ctx := context.Background()

	require.NoError(t, a.RequestRestaurantOrder(ctx, step))
	require.NoError(t, a.CreateDelivery(ctx, step))

	assert.Equal(t, []fulfillorder.RestaurantOrderRequest{{
		OrderID:        orderID,
		RestaurantID:   "restaurant-1",
		Items:          []fulfillorder.RestaurantOrderItem{{MenuItemID: "menu-1", Quantity: 2}},
		IdempotencyKey: step.IdempotencyKey,
	}}, neighbours.restaurants)
	assert.Equal(t, []fulfillorder.DeliveryRequest{{
		OrderID:         orderID,
		RestaurantID:    "restaurant-1",
		DeliveryAddress: "Main street 1",
		IdempotencyKey:  step.IdempotencyKey,
	}}, neighbours.deliveries)
}

func TestActivitiesKeepTransportFailuresRetryable(t *testing.T) {
	a, neighbours, _ := activities(t, order.StatePlaced)
	neighbours.err = errors.New("unavailable")

	err := a.CancelDelivery(context.Background(), step)

	var application *temporal.ApplicationError
	assert.False(t, errors.As(err, &application))
	assert.ErrorIs(t, err, neighbours.err)
}

func TestActivitiesDoNotRetryPermanentErrors(t *testing.T) {
	a, _, _ := activities(t, order.StatePlaced)
	ctx := context.Background()
	tests := []struct {
		name     string
		run      func() error
		wantType string
	}{
		{
			name: "missing order",
			run: func() error {
				_, err := a.CapturePayment(ctx, fulfillorder.StepInput{OrderID: "missing", IdempotencyKey: "k"})
				return err
			},
			wantType: fulfillorder.ErrTypeOrderNotFound,
		},
		{
			name:     "missing idempotency key",
			run:      func() error { return a.RefundPayment(ctx, fulfillorder.StepInput{OrderID: orderID}) },
			wantType: fulfillorder.ErrTypeInvalidInput,
		},
		{
			name:     "completion of a placed order",
			run:      func() error { return a.CompleteOrder(ctx, step) },
			wantType: fulfillorder.ErrTypeInvalidTransition,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var application *temporal.ApplicationError
			require.True(t, errors.As(tt.run(), &application))
			assert.Equal(t, tt.wantType, application.Type())
			assert.True(t, application.NonRetryable())
		})
	}
}

func TestCompleteOrderActivityIsIdempotent(t *testing.T) {
	a, _, store := activities(t, order.StateConfirmed)

	require.NoError(t, a.CompleteOrder(context.Background(), step))
	require.NoError(t, a.CompleteOrder(context.Background(), step))

	snapshot, _ := store.Snapshot(orderID)
	assert.Equal(t, order.StateCompleted, snapshot.State)
	assert.Len(t, store.OutboxRecords(), 1)
}

func TestFailOrCancelOrderActivityMapsOutcome(t *testing.T) {
	tests := []struct {
		outcome fulfillorder.Outcome
		want    order.NegativeOutcome
	}{
		{outcome: fulfillorder.OutcomeCancellation, want: order.OutcomeCancellation},
		{outcome: fulfillorder.OutcomeFailure, want: order.OutcomeFailure},
	}
	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			a, _, store := activities(t, order.StateConfirmed)

			err := a.FailOrCancelOrder(context.Background(), fulfillorder.FinalizeInput{
				OrderID:    orderID,
				Outcome:    tt.outcome,
				ReasonCode: fulfillorder.ReasonDeliveryFailed,
				DecidedAt:  ordertest.PlacedAt.Add(time.Hour),
			})

			require.NoError(t, err)
			snapshot, _ := store.Snapshot(orderID)
			assert.Equal(t, order.StateCancelled, snapshot.State)
			assert.Equal(t, tt.want, snapshot.CancellationReason.Outcome())
		})
	}
}
