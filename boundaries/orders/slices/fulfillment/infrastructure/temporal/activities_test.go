package temporal_test

import (
	"context"
	"errors"
	ports "nomnom/orders/slices/fulfillment/applications/neighbours"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/postgres/postgrestest"
)

type fakeNeighbours struct {
	payments    []ports.PaymentRequest
	restaurants []ports.RestaurantOrderRequest
	deliveries  []ports.DeliveryRequest
	decision    ports.PaymentDecision
	err         error
}

func (f *fakeNeighbours) Authorize(_ context.Context, r ports.PaymentRequest) (ports.PaymentDecision, error) {
	f.payments = append(f.payments, r)
	return f.decision, f.err
}

func (f *fakeNeighbours) Capture(_ context.Context, r ports.PaymentRequest) (ports.PaymentDecision, error) {
	f.payments = append(f.payments, r)
	return f.decision, f.err
}

func (f *fakeNeighbours) CancelAuthorization(_ context.Context, r ports.PaymentRequest) error {
	f.payments = append(f.payments, r)
	return f.err
}

func (f *fakeNeighbours) Refund(_ context.Context, r ports.PaymentRequest) error {
	f.payments = append(f.payments, r)
	return f.err
}

func (f *fakeNeighbours) RequestOrder(_ context.Context, r ports.RestaurantOrderRequest) error {
	f.restaurants = append(f.restaurants, r)
	return f.err
}

func (f *fakeNeighbours) CancelOrder(_ context.Context, r ports.RestaurantOrderRequest) error {
	f.restaurants = append(f.restaurants, r)
	return f.err
}

func (f *fakeNeighbours) Create(_ context.Context, r ports.DeliveryRequest) error {
	f.deliveries = append(f.deliveries, r)
	return f.err
}

func (f *fakeNeighbours) Replan(_ context.Context, r ports.DeliveryRequest) error {
	f.deliveries = append(f.deliveries, r)
	return f.err
}

func (f *fakeNeighbours) Cancel(_ context.Context, r ports.DeliveryRequest) error {
	f.deliveries = append(f.deliveries, r)
	return f.err
}

func activities(t *testing.T, state order.State) (*temporal.Activities, *fakeNeighbours, *postgrestest.Store) {
	t.Helper()
	store := postgrestest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, orderID, ordertest.WithState(state))))
	neighbours := &fakeNeighbours{decision: ports.PaymentDecision{Approved: true}}
	return &temporal.Activities{
		Orders:      store,
		Payments:    neighbours,
		Restaurants: neighbours,
		Delivery:    neighbours,
		Complete:    completeorder.NewHandler(store, store, store),
		Finalize:    failorcancelorder.NewHandler(store, store, store),
	}, neighbours, store
}

var step = temporal.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:authorize_payment"}

var completeStep = temporal.StepInput{
	OrderID:        orderID,
	IdempotencyKey: "order:order-1:complete_order",
	DecidedAt:      ordertest.PlacedAt.Add(time.Hour),
}

func TestPaymentActivitiesUseOrderSnapshotAndStableKey(t *testing.T) {
	a, neighbours, _ := activities(t, order.StatePlaced)
	neighbours.decision = ports.PaymentDecision{Reason: "card_declined"}

	decision, err := a.AuthorizePayment(context.Background(), step)

	require.NoError(t, err, "a decline is a business result, not an error")
	assert.Equal(t, ports.PaymentDecision{Reason: "card_declined"}, decision)
	assert.Equal(t, []ports.PaymentRequest{{
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

	assert.Equal(t, []ports.RestaurantOrderRequest{{
		OrderID:        orderID,
		RestaurantID:   "restaurant-1",
		Items:          []ports.RestaurantOrderItem{{MenuItemID: "menu-1", Quantity: 2}},
		IdempotencyKey: step.IdempotencyKey,
	}}, neighbours.restaurants)
	assert.Equal(t, []ports.DeliveryRequest{{
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

	var application *sdktemporal.ApplicationError
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
				_, err := a.CapturePayment(ctx, temporal.StepInput{OrderID: "missing", IdempotencyKey: "k"})
				return err
			},
			wantType: temporal.ErrTypeOrderNotFound,
		},
		{
			name:     "missing idempotency key",
			run:      func() error { return a.RefundPayment(ctx, temporal.StepInput{OrderID: orderID}) },
			wantType: temporal.ErrTypeInvalidInput,
		},
		{
			name:     "completion of a placed order",
			run:      func() error { return a.CompleteOrder(ctx, completeStep) },
			wantType: temporal.ErrTypeInvalidTransition,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var application *sdktemporal.ApplicationError
			require.True(t, errors.As(tt.run(), &application))
			assert.Equal(t, tt.wantType, application.Type())
			assert.True(t, application.NonRetryable())
		})
	}
}

func TestCompleteOrderActivityIsIdempotent(t *testing.T) {
	a, _, store := activities(t, order.StateConfirmed)

	require.NoError(t, a.CompleteOrder(context.Background(), completeStep))
	require.NoError(t, a.CompleteOrder(context.Background(), completeStep))

	snapshot, _ := store.Snapshot(orderID)
	assert.Equal(t, order.StateCompleted, snapshot.State)
	require.Len(t, store.OutboxRecords(), 1)
	// AC-634: the event time is the completion moment decided by the workflow.
	completedAt, err := time.Parse(time.RFC3339Nano, store.OutboxRecords()[0].OccurredAt)
	require.NoError(t, err)
	assert.True(t, completeStep.DecidedAt.Equal(completedAt))
}

func TestCompleteOrderActivityRequiresDecidedAt(t *testing.T) {
	a, _, store := activities(t, order.StateConfirmed)
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(a.CompleteOrder, activity.RegisterOptions{Name: temporal.ActivityCompleteOrder})

	_, err := env.ExecuteActivity(temporal.ActivityCompleteOrder, temporal.StepInput{OrderID: orderID, IdempotencyKey: "k"})

	require.Error(t, err)
	assert.Empty(t, store.OutboxRecords())
}

func TestFailOrCancelOrderActivityMapsOutcome(t *testing.T) {
	tests := []struct {
		outcome temporal.Outcome
		want    order.NegativeOutcome
	}{
		{outcome: temporal.OutcomeCancellation, want: order.OutcomeCancellation},
		{outcome: temporal.OutcomeFailure, want: order.OutcomeFailure},
	}
	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			a, _, store := activities(t, order.StateConfirmed)

			err := a.FailOrCancelOrder(context.Background(), temporal.FinalizeInput{
				OrderID:    orderID,
				Outcome:    tt.outcome,
				ReasonCode: temporal.ReasonDeliveryFailed,
				DecidedAt:  ordertest.PlacedAt.Add(time.Hour),
			})

			require.NoError(t, err)
			snapshot, _ := store.Snapshot(orderID)
			assert.Equal(t, order.StateCancelled, snapshot.State)
			assert.Equal(t, tt.want, snapshot.CancellationReason.Outcome())
		})
	}
}
