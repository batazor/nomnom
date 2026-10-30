package temporal_test

import (
	"context"
	"errors"
	ports "nomnom/orders/slices/fulfillment/applications/neighbours"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/applications/getfulfillmentorder"
	"nomnom/orders/slices/order/applications/recordpaymentauthorization"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// activities builds Activities over a real store; the neighbours are mocks
// without expectations, so any unexpected neighbour call fails the test.
func activities(t *testing.T, state order.State) (*temporal.Activities, *repositorytest.Store) {
	t.Helper()
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, orderID, ordertest.WithState(state))))
	return &temporal.Activities{
		Orders:        getfulfillmentorder.NewHandler(store),
		Payments:      NewMockPayments(t),
		Restaurants:   NewMockRestaurants(t),
		Delivery:      NewMockDelivery(t),
		RecordPayment: recordpaymentauthorization.NewHandler(store, store),
		Complete:      completeorder.NewHandler(store, store, store),
		Finalize:      failorcancelorder.NewHandler(store, store, store),
	}, store
}

var step = temporal.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:authorize_payment"}

var completeStep = temporal.StepInput{
	OrderID:        orderID,
	IdempotencyKey: "order:order-1:complete_order",
	DecidedAt:      ordertest.PlacedAt.Add(time.Hour),
}

func TestPaymentActivitiesUseOrderSnapshotAndStableKey(t *testing.T) {
	a, _ := activities(t, order.StatePlaced)
	payments := NewMockPayments(t)
	a.Payments = payments
	payments.EXPECT().Authorize(mock.Anything, ports.PaymentRequest{
		OrderID:        orderID,
		CustomerID:     "customer-1",
		MarketID:       ordertest.Market,
		Amount:         "20",
		Currency:       "EUR",
		IdempotencyKey: "order:order-1:authorize_payment",
	}).Return(ports.PaymentDecision{Reason: "card_declined"}, nil).Once()

	decision, err := a.AuthorizePayment(context.Background(), step)

	require.NoError(t, err, "a decline is a business result, not an error")
	assert.Equal(t, ports.PaymentDecision{Reason: "card_declined"}, decision)
}

func TestRestaurantAndDeliveryActivitiesUseOrderSnapshot(t *testing.T) {
	a, _ := activities(t, order.StatePlaced)
	restaurants := NewMockRestaurants(t)
	delivery := NewMockDelivery(t)
	a.Restaurants, a.Delivery = restaurants, delivery
	restaurants.EXPECT().RequestOrder(mock.Anything, ports.RestaurantOrderRequest{
		OrderID:        orderID,
		RestaurantID:   "restaurant-1",
		Items:          []ports.RestaurantOrderItem{{MenuItemID: "menu-1", Quantity: 2}},
		Priority:       ports.PriorityStandard,
		IdempotencyKey: step.IdempotencyKey,
	}).Return("ro-1", nil).Once()
	delivery.EXPECT().Create(mock.Anything, ports.DeliveryRequest{
		OrderID:        orderID,
		MarketID:       ordertest.Market,
		IdempotencyKey: step.IdempotencyKey,
	}).Return(nil).Once()
	ctx := context.Background()

	requested, err := a.RequestRestaurantOrder(ctx, step)
	require.NoError(t, err)
	assert.Equal(t, temporal.RestaurantOrderRequested{RestaurantOrderID: "ro-1"}, requested)
	require.NoError(t, a.CreateDelivery(ctx, step))
}

func TestActivitiesKeepTransportFailuresRetryable(t *testing.T) {
	a, _ := activities(t, order.StatePlaced)
	delivery := NewMockDelivery(t)
	a.Delivery = delivery
	unavailable := errors.New("unavailable")
	delivery.EXPECT().Cancel(mock.Anything, mock.MatchedBy(func(r ports.DeliveryRequest) bool {
		return r.OrderID == orderID && r.IdempotencyKey == step.IdempotencyKey
	})).Return(unavailable).Once()

	err := a.CancelDelivery(context.Background(), step)

	var application *sdktemporal.ApplicationError
	assert.False(t, errors.As(err, &application))
	assert.ErrorIs(t, err, unavailable)
}

func TestActivitiesDoNotRetryPermanentErrors(t *testing.T) {
	a, _ := activities(t, order.StatePlaced)
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

// AC-825: a repeated payment Activity records the fact once with the workflow time.
func TestRecordPaymentAuthorizationActivityIsIdempotent(t *testing.T) {
	a, store := activities(t, order.StatePlaced)
	paymentStep := temporal.StepInput{
		OrderID:        orderID,
		IdempotencyKey: "order:order-1:record_payment_authorization",
		DecidedAt:      ordertest.PlacedAt.Add(time.Second),
	}

	require.NoError(t, a.RecordPaymentAuthorization(context.Background(), paymentStep))
	require.NoError(t, a.RecordPaymentAuthorization(context.Background(), paymentStep))

	snapshot, _ := store.Snapshot(orderID)
	assert.Equal(t, []order.RecordedFact{{Fact: order.FactPaymentAuthorized, OccurredAt: paymentStep.DecidedAt}}, snapshot.Facts)
	assert.Empty(t, store.OutboxRecords())
}

func TestRecordPaymentAuthorizationActivityRequiresDecidedAt(t *testing.T) {
	a, store := activities(t, order.StatePlaced)
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(a.RecordPaymentAuthorization, activity.RegisterOptions{Name: temporal.ActivityRecordPaymentAuthorization})

	_, err := env.ExecuteActivity(temporal.ActivityRecordPaymentAuthorization, temporal.StepInput{OrderID: orderID, IdempotencyKey: "k"})

	require.Error(t, err)
	snapshot, _ := store.Snapshot(orderID)
	assert.Empty(t, snapshot.Facts)
}

func TestCompleteOrderActivityIsIdempotent(t *testing.T) {
	a, store := activities(t, order.StateConfirmed)

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
	a, store := activities(t, order.StateConfirmed)
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
			a, store := activities(t, order.StateConfirmed)

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
