package recordpaymentauthorization_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/recordpaymentauthorization"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var authorizedAt = time.Date(2026, time.October, 27, 12, 0, 5, 0, time.UTC)

func setup(t *testing.T, state order.State) (recordpaymentauthorization.Handler, *repositorytest.Store) {
	t.Helper()
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(state))))
	return recordpaymentauthorization.NewHandler(store, store), store
}

func TestRequiresInput(t *testing.T) {
	handler, _ := setup(t, order.StatePlaced)

	_, err := handler.Handle(context.Background(), recordpaymentauthorization.Input{OrderID: "order-1"})

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}
