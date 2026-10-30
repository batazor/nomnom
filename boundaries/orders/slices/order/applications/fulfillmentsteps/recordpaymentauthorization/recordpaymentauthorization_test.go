package recordpaymentauthorization_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

// AC-829 and AC-830: the payment is recorded once; a retried Activity changes nothing.
func TestRecordsPaymentOnce(t *testing.T) {
	handler, store := setup(t, order.StatePlaced)
	in := recordpaymentauthorization.Input{OrderID: "order-1", AuthorizedAt: authorizedAt}

	first, err := handler.Handle(context.Background(), in)
	require.NoError(t, err)
	retried, err := handler.Handle(context.Background(), recordpaymentauthorization.Input{OrderID: "order-1", AuthorizedAt: authorizedAt.Add(time.Minute)})
	require.NoError(t, err)

	assert.Equal(t, recordpaymentauthorization.Result{Outcome: recordpaymentauthorization.OutcomeRecorded}, first)
	assert.Equal(t, recordpaymentauthorization.Result{Outcome: recordpaymentauthorization.OutcomeAlreadyRecorded}, retried)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.StatePaid, snapshot.State)
	assert.Equal(t, uint64(2), snapshot.Version)
	assert.Equal(t, []order.AppliedTransition{{Trigger: order.TriggerAuthorizePayment, OccurredAt: authorizedAt}}, snapshot.Transitions)
	assert.Empty(t, store.OutboxRecords(), "no Integration Event")
}

// FR-639: an Order past placed is an invalid transition without changes.
func TestRejectsOtherStates(t *testing.T) {
	for _, state := range []order.State{order.StateConfirmed, order.StateCancelled, order.StateCompleted} {
		t.Run(string(state), func(t *testing.T) {
			handler, store := setup(t, state)

			_, err := handler.Handle(context.Background(), recordpaymentauthorization.Input{OrderID: "order-1", AuthorizedAt: authorizedAt})

			var invalid order.InvalidTransition
			require.ErrorAs(t, err, &invalid)
			snapshot, _ := store.Snapshot("order-1")
			assert.Empty(t, snapshot.Transitions)
		})
	}
}

func TestRequiresInput(t *testing.T) {
	handler, _ := setup(t, order.StatePlaced)

	_, err := handler.Handle(context.Background(), recordpaymentauthorization.Input{OrderID: "order-1"})

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}
