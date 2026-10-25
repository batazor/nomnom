package failorcancelorder_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

var decidedAt = time.Date(2026, time.October, 25, 14, 0, 0, 0, time.UTC)

func setup(t *testing.T, state order.State) (failorcancelorder.Handler, *memory.Store) {
	t.Helper()
	store := memory.NewStore()
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(state))))
	return failorcancelorder.NewHandler(store, store, store), store
}

// AC-123: the negative outcome is stored with its kind and reason and published.
func TestFailOrCancelOrderRecordsOutcome(t *testing.T) {
	tests := []struct {
		state   order.State
		outcome order.NegativeOutcome
		code    string
	}{
		{state: order.StatePlaced, outcome: order.OutcomeFailure, code: "restaurant_rejected"},
		{state: order.StateConfirmed, outcome: order.OutcomeCancellation, code: "customer_requested"},
	}
	for _, tt := range tests {
		t.Run(string(tt.state)+"/"+string(tt.outcome), func(t *testing.T) {
			handler, store := setup(t, tt.state)

			result, err := handler.Handle(context.Background(), failorcancelorder.Input{
				OrderID: "order-1", Outcome: tt.outcome, ReasonCode: tt.code, DecidedAt: decidedAt,
			})

			require.NoError(t, err)
			assert.Equal(t, failorcancelorder.Result{Outcome: failorcancelorder.OutcomeCancelled}, result)
			reason, err := order.NewCancellationReason(tt.outcome, tt.code)
			require.NoError(t, err)
			snapshot, _ := store.Snapshot("order-1")
			assert.Equal(t, order.StateCancelled, snapshot.State)
			assert.Equal(t, reason, snapshot.CancellationReason)
			assert.Equal(t, decidedAt, snapshot.CancelledAt)
			assert.Equal(t, []memory.OutboxRecord{{
				AggregateType:    "order",
				AggregateID:      "order-1",
				AggregateVersion: 2,
				Event:            order.OrderCancelled{OrderID: "order-1", RequestedAt: decidedAt, Reason: reason},
			}}, store.OutboxRecords())
		})
	}
}

// AC-124: a repeated Activity is a no-op.
func TestFailOrCancelOrderRepeatIsNoOp(t *testing.T) {
	handler, store := setup(t, order.StateConfirmed)
	in := failorcancelorder.Input{OrderID: "order-1", Outcome: order.OutcomeFailure, ReasonCode: "delivery_failed", DecidedAt: decidedAt}

	_, err := handler.Handle(context.Background(), in)
	require.NoError(t, err)
	retried, err := handler.Handle(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, failorcancelorder.Result{Outcome: failorcancelorder.OutcomeAlreadyCancelled}, retried)
	assert.Len(t, store.OutboxRecords(), 1)
}

// AC-125: a completed Order cannot receive a negative outcome.
func TestFailOrCancelOrderRejectsCompletedOrder(t *testing.T) {
	handler, store := setup(t, order.StateCompleted)

	_, err := handler.Handle(context.Background(), failorcancelorder.Input{
		OrderID: "order-1", Outcome: order.OutcomeCancellation, ReasonCode: "customer_requested", DecidedAt: decidedAt,
	})

	assert.Equal(t, order.InvalidTransition{From: order.StateCompleted, Trigger: order.TriggerCancel}, err)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.StateCompleted, snapshot.State)
	assert.Empty(t, store.OutboxRecords())
}

func TestFailOrCancelOrderValidatesInput(t *testing.T) {
	handler, _ := setup(t, order.StatePlaced)
	tests := []struct {
		name    string
		in      failorcancelorder.Input
		wantErr error
	}{
		{name: "no order", in: failorcancelorder.Input{Outcome: order.OutcomeFailure, ReasonCode: "x", DecidedAt: decidedAt}, wantErr: application.ErrInvalidInput},
		{name: "no time", in: failorcancelorder.Input{OrderID: "order-1", Outcome: order.OutcomeFailure, ReasonCode: "x"}, wantErr: application.ErrInvalidInput},
		{name: "unknown outcome", in: failorcancelorder.Input{OrderID: "order-1", Outcome: "refund", ReasonCode: "x", DecidedAt: decidedAt}, wantErr: order.ErrInvalidCancellationReason},
		{name: "no reason", in: failorcancelorder.Input{OrderID: "order-1", Outcome: order.OutcomeFailure, DecidedAt: decidedAt}, wantErr: order.ErrInvalidCancellationReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := handler.Handle(context.Background(), tt.in)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
