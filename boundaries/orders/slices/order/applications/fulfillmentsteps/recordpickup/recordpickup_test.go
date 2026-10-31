package recordpickup_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/recordpickup"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

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
