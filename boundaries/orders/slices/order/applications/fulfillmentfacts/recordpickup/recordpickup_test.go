package recordpickup_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/fulfillmentfacts/recordpickup"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
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
		{name: "placed", outcome: fulfillment.OutcomeApplied, wantState: order.StatePickedUp, wantFacts: []order.RecordedFact{recorded}},
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := repositorytest.Open(t)
			workflow := NewMockNotifier(t)
			workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
				OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
			}).Return(nil).Once()
			require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", tt.options...)))
			handler := recordpickup.NewHandler(store, store, store, workflow)

			result, err := handler.Handle(context.Background(), input("event-1"))

			require.NoError(t, err)
			assert.Equal(t, recordpickup.Result{Outcome: tt.outcome, WorkflowNotified: true}, result)
			snapshot, _ := store.Snapshot("order-1")
			assert.Equal(t, tt.wantState, snapshot.State)
			assert.Equal(t, tt.wantFacts, snapshot.Facts)
			assert.Equal(t, 1, store.InboxSize())
		})
	}
}

// FR-611: Pickup stores the handover time named by the courier.
func TestHandleStoresExpectedDeliveredTime(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed))))
	in := input("event-1")
	in.ExpectedDeliveredAt = occurredAt.Add(20 * time.Minute)
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindPickedUp, OccurredAt: occurredAt,
	}).Return(nil).Once()

	result, err := recordpickup.NewHandler(store, store, store, workflow).Handle(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeApplied, result.Outcome)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.ExpectedTimes{DeliveredAt: in.ExpectedDeliveredAt}, snapshot.Expected)
}
