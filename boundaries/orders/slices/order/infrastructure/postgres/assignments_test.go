package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

// AC-523: the Courier Assignment replaces the previous one with its revision
// and is the source of the delivery relation; an Order without assignment has
// no delivery subject.
func TestCourierAssignment(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("assigned")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))

	_, found, err := store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	assert.False(t, found)
	subjects, err := store.SubjectsOf(ctx, postgres.RelationDelivery, []string{string(id)})
	require.NoError(t, err)
	assert.Empty(t, subjects, "an Order without assignment has no delivery subject")

	at := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	for version, courier := range []string{"courier-1", "courier-2"} {
		err := store.Do(ctx, func(ctx context.Context) error {
			if _, _, err := store.CourierAssignment(ctx, id); err != nil {
				return err
			}
			return store.RememberCourierAssignment(ctx, id, order.CourierAssignment{
				DeliveryID: "delivery-1", CourierID: courier, AssignedAt: at, DeliveryVersion: uint64(version) + 2,
			}, "zed-"+courier)
		})
		require.NoError(t, err)
	}

	stored, found, err := store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-2", AssignedAt: at, DeliveryVersion: 3}, stored)
	revision, _, err := store.Revision(ctx, string(id))
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("zed-courier-2"), revision)

	for relation, want := range map[postgres.Relation]string{
		postgres.RelationCustomer: "customer-1", postgres.RelationRestaurant: "restaurant-1",
		postgres.RelationDelivery: "delivery-1",
	} {
		subjects, err := store.SubjectsOf(ctx, relation, []string{string(id)})
		require.NoError(t, err)
		assert.Equal(t, want, subjects[string(id)], relation)
	}
	page, err := store.RelationPage(ctx, postgres.RelationDelivery, string(id)[:len(id)-1], 1000)
	require.NoError(t, err)
	assert.Contains(t, page, postgres.RelationSubject{OrderID: string(id), SubjectID: "delivery-1"})
	for _, relation := range []postgres.Relation{"owner", "courier"} {
		_, err = store.SubjectsOf(ctx, relation, []string{string(id)})
		require.Error(t, err, "%s is not a reconciled relation", relation)
	}
}

// AC-534, AC-535: a released assignment keeps its time and version and stores
// the cleared courier (and, for a cancellation, delivery) as NULL; the delivery
// relation stays until the Delivery is cancelled.
func TestCourierRelease(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("released")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	at := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	remember := func(assignment order.CourierAssignment, revision string) {
		t.Helper()
		require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
			return store.RememberCourierAssignment(ctx, id, assignment, revision)
		}))
	}
	subject := func(relation postgres.Relation) (string, bool) {
		t.Helper()
		subjects, err := store.SubjectsOf(ctx, relation, []string{string(id)})
		require.NoError(t, err)
		s, ok := subjects[string(id)]
		return s, ok
	}

	remember(order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: at, DeliveryVersion: 2}, "zed-assigned")
	remember(order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: at.Add(time.Minute), DeliveryVersion: 3}, "zed-unassigned")
	stored, found, err := store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	require.True(t, found, "a released assignment still orders later events")
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: at.Add(time.Minute), DeliveryVersion: 3}, stored)
	delivery, ok := subject(postgres.RelationDelivery)
	assert.True(t, ok)
	assert.Equal(t, "delivery-1", delivery)
	revision, _, err := store.Revision(ctx, string(id))
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("zed-unassigned"), revision)

	remember(order.CourierAssignment{AssignedAt: at.Add(2 * time.Minute), DeliveryVersion: 4}, "zed-cancelled")
	stored, found, err = store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{AssignedAt: at.Add(2 * time.Minute), DeliveryVersion: 4}, stored)
	_, ok = subject(postgres.RelationDelivery)
	assert.False(t, ok, "the cancelled delivery is absent")
}

// AC-667: RestoreRelation runs the restore and stores its revision only while
// the committed Order still names the subject.
func TestRestoreRelation(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("restore")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	restores := 0
	restore := func(context.Context) (application.AuthorizationRevision, error) {
		restores++
		return application.AuthorizationRevision("zed-restored"), nil
	}

	restored, err := store.RestoreRelation(ctx, postgres.RelationCustomer, string(id), "customer-other", restore)
	require.NoError(t, err)
	assert.False(t, restored, "another subject is not restored")
	restored, err = store.RestoreRelation(ctx, postgres.RelationDelivery, string(id), "delivery-1", restore)
	require.NoError(t, err)
	assert.False(t, restored, "an Order without Delivery restores none")
	restored, err = store.RestoreRelation(ctx, postgres.RelationCustomer, "order-unknown", "customer-1", restore)
	require.NoError(t, err)
	assert.False(t, restored, "an absent Order restores nothing")
	assert.Zero(t, restores)

	restored, err = store.RestoreRelation(ctx, postgres.RelationCustomer, string(id), "customer-1", restore)
	require.NoError(t, err)
	assert.True(t, restored)
	assert.Equal(t, 1, restores)
	revision, _, err := store.Revision(ctx, string(id))
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("zed-restored"), revision)
}
