package confirm_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/confirm"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func setup(t *testing.T, state order.State) (confirm.Handler, *repositorytest.Store, *MockNotifier) {
	t.Helper()
	store := repositorytest.Open(t)
	workflow := NewMockNotifier(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithState(state))))
	return confirm.NewHandler(store, store, store, workflow), store, workflow
}

func input(eventID string) confirm.Input {
	return confirm.Input{EventID: eventID, OrderID: "order-1", OccurredAt: occurredAt}
}

// FR-611: acceptance stores the ready time named by the restaurant.
func TestConfirmOrderStoresExpectedReadyTime(t *testing.T) {
	handler, store, workflow := setup(t, order.StatePlaced)
	in := input("event-1")
	in.ExpectedReadyAt = occurredAt.Add(12 * time.Minute)
	workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "event-1", Kind: fulfillment.KindRestaurantAccepted, OccurredAt: occurredAt,
		}).
		Return(nil).
		Once()

	_, err := handler.Handle(context.Background(), in)

	require.NoError(t, err)
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, order.ExpectedTimes{ReadyAt: in.ExpectedReadyAt}, snapshot.Expected)
}
