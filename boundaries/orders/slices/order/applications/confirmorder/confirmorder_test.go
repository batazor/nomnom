package confirmorder_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/confirmorder"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/postgres/postgrestest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func setup(t *testing.T, state order.State) (confirmorder.Handler, *postgrestest.Store, *fake.Workflow) {
	t.Helper()
	store := postgrestest.Open(t)
	workflow := fake.NewWorkflow()
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
		{name: "completed is late", state: order.StateCompleted, outcome: fulfillment.OutcomeIgnoredLate, wantState: order.StateCompleted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, store, workflow := setup(t, tt.state)

			result, err := handler.Handle(context.Background(), input("event-1"))

			require.NoError(t, err)
			assert.Equal(t, confirmorder.Result{Outcome: tt.outcome, WorkflowNotified: true}, result)
			snapshot, _ := store.Snapshot("order-1")
			assert.Equal(t, tt.wantState, snapshot.State)
			assert.Equal(t, []fulfillment.Notification{{
				OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
			}}, workflow.Notifications())
			assert.Empty(t, store.OutboxRecords())
		})
	}
}

// AC-563: the moment of Order Acceptance is kept as the restaurant_accepted fact.
func TestConfirmOrderRecordsAcceptanceMoment(t *testing.T) {
	handler, store, _ := setup(t, order.StatePlaced)

	_, err := handler.Handle(context.Background(), input("event-1"))

	require.NoError(t, err)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, []order.RecordedFact{{Fact: order.FactRestaurantAccepted, OccurredAt: occurredAt}}, snapshot.Facts)
}

// confirmorder.feature @AC-003: acceptance received again has no additional business effect.
func TestConfirmOrderReceivedAgainHasNoAdditionalEffect(t *testing.T) {
	handler, store, workflow := setup(t, order.StatePlaced)

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
	assert.Len(t, workflow.Notifications(), 3, "the workflow deduplicates by event_id")
}

func TestConfirmOrderRequiresEventIdentity(t *testing.T) {
	handler, store, _ := setup(t, order.StatePlaced)

	_, err := handler.Handle(context.Background(), confirmorder.Input{OrderID: "order-1", OccurredAt: occurredAt})

	require.ErrorIs(t, err, application.ErrInvalidInput)
	assert.Zero(t, store.InboxSize())
}
