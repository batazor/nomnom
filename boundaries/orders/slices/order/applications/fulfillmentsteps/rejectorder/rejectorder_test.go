package rejectorder_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/rejectorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func input(eventID string) rejectorder.Input {
	return rejectorder.Input{EventID: eventID, OrderID: "order-1", OccurredAt: occurredAt, Reason: "restaurant_closed"}
}

// AC-109: the step is recorded once in a non-final Order in the history.
func TestHandleByOrderState(t *testing.T) {
	recorded := order.AppliedTransition{Trigger: order.TriggerReject, OccurredAt: occurredAt}
	tests := []struct {
		name            string
		options         []ordertest.Option
		outcome         fulfillment.Outcome
		wantState       order.State
		wantTransitions []order.AppliedTransition
	}{
		{name: "placed", outcome: fulfillment.OutcomeApplied, wantState: order.StateRejected, wantTransitions: []order.AppliedTransition{recorded}},
		{
			name:            "already recorded",
			options:         []ordertest.Option{ordertest.WithTransitions(recorded)},
			outcome:         fulfillment.OutcomeAlreadyApplied,
			wantState:       order.StatePlaced,
			wantTransitions: []order.AppliedTransition{recorded},
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
				OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantRejected, OccurredAt: occurredAt, Reason: "restaurant_closed",
			}).Return(nil).Once()
			require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", tt.options...)))
			handler := rejectorder.NewHandler(store, store, store, workflow)

			result, err := handler.Handle(context.Background(), input("event-1"))

			require.NoError(t, err)
			assert.Equal(t, rejectorder.Result{Outcome: tt.outcome, WorkflowNotified: true}, result)
			snapshot, _ := store.Snapshot("order-1")
			assert.Equal(t, tt.wantState, snapshot.State)
			assert.Equal(t, tt.wantTransitions, snapshot.Transitions)
			assert.Equal(t, 1, store.InboxSize())
		})
	}
}
