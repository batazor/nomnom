package completeorder_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

func setup(t *testing.T, state order.State) (completeorder.Handler, *repositorytest.Store) {
	t.Helper()
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(state))))
	return completeorder.NewHandler(store, store, store), store
}

var completedAt = time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)

// AC-120 and AC-121: a confirmed Order completes once with one OrderCompleted.
func TestCompleteOrderOnceWithOutboxRecord(t *testing.T) {
	handler, store := setup(t, order.StateConfirmed)
	in := completeorder.Input{OrderID: "order-1", CompletedAt: completedAt}

	first, err := handler.Handle(context.Background(), in)
	require.NoError(t, err)
	// A retry carries the same moment; even another one adds no event.
	retried, err := handler.Handle(context.Background(), completeorder.Input{OrderID: "order-1", CompletedAt: completedAt.Add(time.Minute)})
	require.NoError(t, err)

	assert.Equal(t, completeorder.Result{Outcome: completeorder.OutcomeCompleted}, first)
	assert.Equal(t, completeorder.Result{Outcome: completeorder.OutcomeAlreadyCompleted}, retried)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.StateCompleted, snapshot.State)
	assert.Equal(t, uint64(2), snapshot.Version)
	assert.Equal(t, repositorytest.ExpectedOutbox(t, "order-1", 2,
		order.OrderCompleted{OrderID: "order-1", MarketID: ordertest.MarketID(t), PlacedAt: ordertest.PlacedAt, CompletedAt: completedAt},
	), store.OutboxRecords())
}

// AC-122: other states are a state conflict without changes.
func TestCompleteOrderRejectsOtherStates(t *testing.T) {
	for _, state := range []order.State{order.StatePlaced, order.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			handler, store := setup(t, state)

			_, err := handler.Handle(context.Background(), completeorder.Input{OrderID: "order-1", CompletedAt: completedAt})

			assert.Equal(t, order.InvalidTransition{From: state, Trigger: order.TriggerComplete}, err)
			snapshot, _ := store.Snapshot("order-1")
			assert.Equal(t, state, snapshot.State)
			assert.Empty(t, store.OutboxRecords())
		})
	}
}

func TestCompleteOrderInputAndMissingOrder(t *testing.T) {
	handler, _ := setup(t, order.StateConfirmed)

	_, err := handler.Handle(context.Background(), completeorder.Input{CompletedAt: completedAt})
	require.ErrorIs(t, err, usecase.ErrInvalidInput)

	_, err = handler.Handle(context.Background(), completeorder.Input{OrderID: "order-1"})
	require.ErrorIs(t, err, usecase.ErrInvalidInput)

	_, err = handler.Handle(context.Background(), completeorder.Input{OrderID: "missing", CompletedAt: completedAt})
	require.ErrorIs(t, err, order.ErrOrderNotFound)
}
