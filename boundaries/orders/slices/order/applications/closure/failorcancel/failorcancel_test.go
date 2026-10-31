package failorcancel_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/closure/failorcancel"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var decidedAt = time.Date(2026, time.October, 25, 14, 0, 0, 0, time.UTC)

func setup(t *testing.T, state order.State) (failorcancel.Handler, *repositorytest.Store) {
	t.Helper()
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(state))))
	return failorcancel.NewHandler(store, store, store), store
}

func TestFailOrCancelOrderValidatesInput(t *testing.T) {
	handler, _ := setup(t, order.StatePlaced)
	tests := []struct {
		name    string
		in      failorcancel.Input
		wantErr error
	}{
		{name: "no order", in: failorcancel.Input{Outcome: order.OutcomeFailure, ReasonCode: "x", DecidedAt: decidedAt}, wantErr: usecase.ErrInvalidInput},
		{name: "no time", in: failorcancel.Input{OrderID: "order-1", Outcome: order.OutcomeFailure, ReasonCode: "x"}, wantErr: usecase.ErrInvalidInput},
		{name: "unknown outcome", in: failorcancel.Input{OrderID: "order-1", Outcome: "refund", ReasonCode: "x", DecidedAt: decidedAt}, wantErr: order.ErrInvalidCancellationReason},
		{name: "no reason", in: failorcancel.Input{OrderID: "order-1", Outcome: order.OutcomeFailure, DecidedAt: decidedAt}, wantErr: order.ErrInvalidCancellationReason},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := handler.Handle(context.Background(), tt.in)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
