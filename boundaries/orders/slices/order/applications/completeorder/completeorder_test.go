package completeorder_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

func setup(t *testing.T, state order.State) (completeorder.Handler, *memory.Store) {
	t.Helper()
	store := memory.NewStore()
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(state))))
	return completeorder.NewHandler(store, store, store), store
}

// AC-120 and AC-121: a confirmed Order completes once with one OrderCompleted.
func TestCompleteOrderOnceWithOutboxRecord(t *testing.T) {
	handler, store := setup(t, order.StateConfirmed)
	in := completeorder.Input{OrderID: "order-1"}

	first, err := handler.Handle(context.Background(), in)
	require.NoError(t, err)
	retried, err := handler.Handle(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, completeorder.Result{Outcome: completeorder.OutcomeCompleted}, first)
	assert.Equal(t, completeorder.Result{Outcome: completeorder.OutcomeAlreadyCompleted}, retried)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.StateCompleted, snapshot.State)
	assert.Equal(t, uint64(2), snapshot.Version)
	assert.Equal(t, []memory.OutboxRecord{{
		AggregateType:    "order",
		AggregateID:      "order-1",
		AggregateVersion: 2,
		Event:            order.OrderCompleted{OrderID: "order-1", PlacedAt: ordertest.PlacedAt},
	}}, store.OutboxRecords())
}

// AC-122: other states are a state conflict without changes.
func TestCompleteOrderRejectsOtherStates(t *testing.T) {
	for _, state := range []order.State{order.StatePlaced, order.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			handler, store := setup(t, state)

			_, err := handler.Handle(context.Background(), completeorder.Input{OrderID: "order-1"})

			assert.Equal(t, order.InvalidTransition{From: state, Trigger: order.TriggerComplete}, err)
			snapshot, _ := store.Snapshot("order-1")
			assert.Equal(t, state, snapshot.State)
			assert.Empty(t, store.OutboxRecords())
		})
	}
}

func TestCompleteOrderInputAndMissingOrder(t *testing.T) {
	handler, _ := setup(t, order.StateConfirmed)

	_, err := handler.Handle(context.Background(), completeorder.Input{})
	require.ErrorIs(t, err, application.ErrInvalidInput)

	_, err = handler.Handle(context.Background(), completeorder.Input{OrderID: "missing"})
	require.ErrorIs(t, err, order.ErrOrderNotFound)
}
