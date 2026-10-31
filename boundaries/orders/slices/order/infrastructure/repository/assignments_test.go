package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/syncrelationships"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
)

// subjectsOf reads the relationship subjects of an Order in its own
// transaction, as Sync Order Relationships does.
func subjectsOf(t *testing.T, store interface {
	Do(context.Context, func(context.Context) error) error
	RelationshipSubjects(context.Context, order.ID) (syncrelationships.Subjects, error)
}, id order.ID) syncrelationships.Subjects {
	t.Helper()
	var subjects syncrelationships.Subjects
	require.NoError(t, store.Do(context.Background(), func(ctx context.Context) error {
		var err error
		subjects, err = store.RelationshipSubjects(ctx, id)
		return err
	}))
	return subjects
}

// AC-523: the Courier Assignment replaces the previous one and is the source
// of order#delivery; an Order without assignment has no delivery subject.
func TestCourierAssignment(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("assigned")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))

	_, found, err := store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, syncrelationships.Subjects{CustomerID: "customer-1", RestaurantID: "restaurant-1"}, subjectsOf(t, store, id),
		"an Order without assignment has no delivery subject")

	at := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	for version, courier := range []string{"courier-1", "courier-2"} {
		err := store.Do(ctx, func(ctx context.Context) error {
			if _, _, err := store.CourierAssignment(ctx, id); err != nil {
				return err
			}
			return store.RememberCourierAssignment(ctx, id, order.CourierAssignment{
				DeliveryID: "delivery-1", CourierID: courier, AssignedAt: at, DeliveryVersion: uint64(version) + 2,
			})
		})
		require.NoError(t, err)
	}

	stored, found, err := store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-2", AssignedAt: at, DeliveryVersion: 3}, stored)
	assert.Equal(t, syncrelationships.Subjects{CustomerID: "customer-1", RestaurantID: "restaurant-1", DeliveryID: "delivery-1"},
		subjectsOf(t, store, id))

	err = store.Do(ctx, func(ctx context.Context) error {
		_, err := store.RelationshipSubjects(ctx, "order-unknown")
		return err
	})
	require.ErrorIs(t, err, order.ErrOrderNotFound)
}

// AC-534, AC-535: a released assignment keeps its time and version and stores
// the cleared courier (and, for a cancellation, delivery) as NULL; the delivery
// subject stays until the Delivery is cancelled.
func TestCourierRelease(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("released")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	at := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	remember := func(assignment order.CourierAssignment) {
		t.Helper()
		require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
			return store.RememberCourierAssignment(ctx, id, assignment)
		}))
	}

	remember(order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: at, DeliveryVersion: 2})
	remember(order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: at.Add(time.Minute), DeliveryVersion: 3})
	stored, found, err := store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	require.True(t, found, "a released assignment still orders later events")
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: at.Add(time.Minute), DeliveryVersion: 3}, stored)
	assert.Equal(t, "delivery-1", subjectsOf(t, store, id).DeliveryID)

	remember(order.CourierAssignment{AssignedAt: at.Add(2 * time.Minute), DeliveryVersion: 4})
	stored, found, err = store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{AssignedAt: at.Add(2 * time.Minute), DeliveryVersion: 4}, stored)
	assert.Empty(t, subjectsOf(t, store, id).DeliveryID, "the cancelled delivery is absent")
}

// OrderIDs pages every Order by id for orders-admin authz resync.
func TestOrderIDsPagesAllOrders(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	first, second := uniqueID("page-a"), uniqueID("page-b")
	require.NoError(t, store.Add(ctx, ordertest.New(t, first)))
	require.NoError(t, store.Add(ctx, ordertest.New(t, second)))

	ids, err := store.OrderIDs(ctx, "", 100000)
	require.NoError(t, err)
	assert.Contains(t, ids, first)
	assert.Contains(t, ids, second)
	after, err := store.OrderIDs(ctx, first, 1)
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Greater(t, string(after[0]), string(first))
}
