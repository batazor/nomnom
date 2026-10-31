package completeorder_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/closure/completeorder"
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

func TestCompleteOrderInputAndMissingOrder(t *testing.T) {
	handler, _ := setup(t, order.StateDelivered)

	_, err := handler.Handle(context.Background(), completeorder.Input{CompletedAt: completedAt})
	require.ErrorIs(t, err, usecase.ErrInvalidInput)

	_, err = handler.Handle(context.Background(), completeorder.Input{OrderID: "order-1"})
	require.ErrorIs(t, err, usecase.ErrInvalidInput)

	_, err = handler.Handle(context.Background(), completeorder.Input{OrderID: "missing", CompletedAt: completedAt})
	require.ErrorIs(t, err, order.ErrOrderNotFound)
}
