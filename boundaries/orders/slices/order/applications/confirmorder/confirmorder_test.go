package confirmorder_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/confirmorder"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func setup(t *testing.T, state order.State) (confirmorder.Handler, *repositorytest.Store, *MockNotifier) {
	t.Helper()
	store := repositorytest.Open(t)
	workflow := NewMockNotifier(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(state))))
	return confirmorder.NewHandler(store, store, store, workflow), store, workflow
}

func input(eventID string) confirmorder.Input {
	return confirmorder.Input{EventID: eventID, OrderID: "order-1", OccurredAt: occurredAt}
}

// AC-108 (confirmorder.feature @AC-001): acceptance confirms the Order and lets fulfillment continue.
func TestConfirmOrderByState(t *testing.T) {
	tests := []struct {
		name      string
		state     order.State
		outcome   fulfillment.Outcome
		wantState order.State
	}{
		{name: "placed is confirmed", state: order.StatePlaced, outcome: fulfillment.OutcomeApplied, wantState: order.StateConfirmed},
		{name: "confirmed stays confirmed", state: order.StateConfirmed, outcome: fulfillment.OutcomeAlreadyApplied, wantState: order.StateConfirmed},
		{name: "cancelled is late", state: order.StateCancelled, outcome: fulfillment.OutcomeIgnoredLate, wantState: order.StateCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, store, workflow := setup(t, tt.state)
			workflow.EXPECT().
				Notify(mock.Anything, fulfillment.Notification{
					OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
				}).
				Return(nil).
				Once()

			result, err := handler.Handle(context.Background(), input("event-1"))

			require.NoError(t, err)
			assert.Equal(t, confirmorder.Result{Outcome: tt.outcome, WorkflowNotified: true}, result)
			snapshot, _ := store.Snapshot("order-1")
			assert.Equal(t, tt.wantState, snapshot.State)
			assert.Empty(t, store.OutboxRecords())
		})
	}
}

// AC-563: the moment of Order Acceptance is kept as the restaurant_accepted fact.
func TestConfirmOrderRecordsAcceptanceMoment(t *testing.T) {
	handler, store, workflow := setup(t, order.StatePlaced)
	workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
		}).
		Return(nil).
		Once()

	_, err := handler.Handle(context.Background(), input("event-1"))

	require.NoError(t, err)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, []order.RecordedFact{{Fact: order.FactRestaurantAccepted, OccurredAt: occurredAt}}, snapshot.Facts)
}

// confirmorder.feature @AC-003: acceptance received again has no additional business effect.
func TestConfirmOrderReceivedAgainHasNoAdditionalEffect(t *testing.T) {
	handler, store, workflow := setup(t, order.StatePlaced)
	// Every delivery notifies the workflow; the workflow deduplicates by event_id.
	workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
		}).
		Return(nil).
		Times(2)
	workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-2", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
		}).
		Return(nil).
		Once()

	_, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)
	redelivered, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)
	republished, err := handler.Handle(context.Background(), input("event-2"))
	require.NoError(t, err)

	assert.Equal(t, fulfillment.OutcomeDuplicate, redelivered.Outcome)
	assert.Equal(t, fulfillment.OutcomeAlreadyApplied, republished.Outcome)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.StateConfirmed, snapshot.State)
	assert.Equal(t, uint64(2), snapshot.Version, "only the first event saves the Order")
}

// FR-611: acceptance stores the ready time named by the restaurant.
func TestConfirmOrderStoresExpectedReadyTime(t *testing.T) {
	handler, store, workflow := setup(t, order.StatePlaced)
	in := input("event-1")
	in.ExpectedReadyAt = occurredAt.Add(12 * time.Minute)
	workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
		}).
		Return(nil).
		Once()

	_, err := handler.Handle(context.Background(), in)

	require.NoError(t, err)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.ExpectedTimes{ReadyAt: in.ExpectedReadyAt}, snapshot.Expected)
}
