package di

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/delivery/recordcourierassignment"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/kitchen/recordpreparationdelay"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// changedVersions are the Order versions announced on orders.v1.order-changes.
func changedVersions(store *repositorytest.Store) []uint64 {
	var versions []uint64
	for _, record := range store.OutboxRecords() {
		if record.Topic == outbound.OrderChangesTopic {
			versions = append(versions, record.AggregateVersion)
		}
	}
	return versions
}

// FR-611, ADR 36: a new expected time saves the Order, so orderChanged gets a
// change notification; a reassignment with a new pickup time too.
func TestExpectedTimeChangesAreAnnounced(t *testing.T) {
	db := repositorytest.Open(t)
	store := NewProcessStore(db.Store, observability.NewDomainMetrics(nil))
	ctx := context.Background()
	at := ordertest.PlacedAt.Add(time.Minute)
	require.NoError(t, store.Do(ctx, func(ctx context.Context) error { return store.Add(ctx, ordertest.New(t, "order-1")) }))

	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "delayed", Kind: fulfillment.KindPreparationDelayed, OccurredAt: at, ExpectedReadyAt: at.Add(20 * time.Minute),
	}).Return(nil).Once()
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "assigned-2", Kind: fulfillment.KindCourierAssigned, OccurredAt: at,
	}).Return(nil).Once()
	workflow.EXPECT().Notify(mock.Anything, fulfillment.Notification{
		OrderID: "order-1", EventID: "assigned-3", Kind: fulfillment.KindCourierAssigned, OccurredAt: at,
	}).Return(nil).Once()

	_, err := recordpreparationdelay.NewHandler(store, store, store, workflow).Handle(ctx, recordpreparationdelay.Input{
		EventID: "delayed", OrderID: "order-1", OccurredAt: at, ExpectedReadyAt: at.Add(20 * time.Minute),
	})
	require.NoError(t, err)
	assign := recordcourierassignment.NewHandler(store, store, store, workflow, store, store)
	for _, version := range []uint64{2, 3} {
		_, err := assign.Handle(ctx, recordcourierassignment.Input{
			EventID: fmt.Sprintf("assigned-%d", version), OrderID: "order-1", OccurredAt: at, DeliveryVersion: version,
			DeliveryID: "delivery-1", CourierID: fmt.Sprintf("courier-%d", version), ExpectedPickupAt: at.Add(time.Duration(version) * 10 * time.Minute),
		})
		require.NoError(t, err)
	}

	assert.Equal(t, []uint64{1, 2, 3, 4}, changedVersions(db))
}
