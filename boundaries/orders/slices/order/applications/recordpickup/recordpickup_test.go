package recordpickup_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordpickup"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/postgres/postgrestest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func input(eventID string) recordpickup.Input {
	return recordpickup.Input{EventID: eventID, OrderID: "order-1", OccurredAt: occurredAt}
}

// AC-110: the fact is recorded once in a non-final Order without changing its state.
func TestHandleByOrderState(t *testing.T) {
	recorded := order.RecordedFact{Fact: order.FactPickedUp, OccurredAt: occurredAt}
	tests := []struct {
		name      string
		options   []ordertest.Option
		outcome   fulfillment.Outcome
		wantState order.State
		wantFacts []order.RecordedFact
	}{
		{name: "placed", outcome: fulfillment.OutcomeApplied, wantState: order.StatePlaced, wantFacts: []order.RecordedFact{recorded}},
		{
			name:      "confirmed",
			options:   []ordertest.Option{ordertest.WithState(order.StateConfirmed)},
			outcome:   fulfillment.OutcomeApplied,
			wantState: order.StateConfirmed,
			wantFacts: []order.RecordedFact{recorded},
		},
		{
			name:      "already recorded",
			options:   []ordertest.Option{ordertest.WithFacts(recorded)},
			outcome:   fulfillment.OutcomeAlreadyApplied,
			wantState: order.StatePlaced,
			wantFacts: []order.RecordedFact{recorded},
		},
		{
			name:      "cancelled is late",
			options:   []ordertest.Option{ordertest.WithState(order.StateCancelled)},
			outcome:   fulfillment.OutcomeIgnoredLate,
			wantState: order.StateCancelled,
		},
		{
			name:      "completed is late",
			options:   []ordertest.Option{ordertest.WithState(order.StateCompleted)},
			outcome:   fulfillment.OutcomeIgnoredLate,
			wantState: order.StateCompleted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := postgrestest.Open(t)
			workflow := fake.NewWorkflow()
			require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", tt.options...)))
			handler := recordpickup.NewHandler(store, store, store, workflow)

			result, err := handler.Handle(context.Background(), input("event-1"))

			require.NoError(t, err)
			assert.Equal(t, recordpickup.Result{Outcome: tt.outcome, WorkflowNotified: true}, result)
			snapshot, _ := store.Snapshot("order-1")
			assert.Equal(t, tt.wantState, snapshot.State)
			assert.Equal(t, tt.wantFacts, snapshot.Facts)
			assert.Equal(t, []fulfillment.Notification{{
				OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
			}}, workflow.Notifications())
			assert.Equal(t, 1, store.InboxSize())
		})
	}
}

func TestHandleRedeliveryIsDuplicate(t *testing.T) {
	store := postgrestest.Open(t)
	workflow := fake.NewWorkflow()
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1")))
	handler := recordpickup.NewHandler(store, store, store, workflow)

	_, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)
	result, err := handler.Handle(context.Background(), input("event-1"))
	require.NoError(t, err)

	assert.Equal(t, fulfillment.OutcomeDuplicate, result.Outcome)
	snapshot, _ := store.Snapshot("order-1")
	assert.Len(t, snapshot.Facts, 1)
	assert.Len(t, workflow.Notifications(), 2)
}

func TestHandleRequiresOccurredAt(t *testing.T) {
	store := postgrestest.Open(t)
	handler := recordpickup.NewHandler(store, store, store, fake.NewWorkflow())

	_, err := handler.Handle(context.Background(), recordpickup.Input{EventID: "event-1", OrderID: "order-1"})

	require.ErrorIs(t, err, application.ErrInvalidInput)
}
