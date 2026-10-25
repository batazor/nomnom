//go:build integration

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
// and is the source of the courier and delivery relations.
func TestCourierAssignment(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("assigned")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))

	_, found, err := store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	assert.False(t, found)
	subjects, err := store.SubjectsOf(ctx, postgres.RelationCourier, []string{string(id)})
	require.NoError(t, err)
	assert.Empty(t, subjects, "an Order without courier has no courier subject")

	at := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	for _, courier := range []string{"courier-1", "courier-2"} {
		err := store.Do(ctx, func(ctx context.Context) error {
			if _, _, err := store.CourierAssignment(ctx, id); err != nil {
				return err
			}
			return store.RememberCourierAssignment(ctx, id, order.CourierAssignment{
				DeliveryID: "delivery-1", CourierID: courier, AssignedAt: at,
			}, "zed-"+courier)
		})
		require.NoError(t, err)
	}

	stored, found, err := store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-2", AssignedAt: at}, stored)
	revision, _, err := store.Revision(ctx, string(id))
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("zed-courier-2"), revision)

	for relation, want := range map[postgres.Relation]string{
		postgres.RelationCustomer: "customer-1", postgres.RelationRestaurant: "restaurant-1",
		postgres.RelationCourier: "courier-2", postgres.RelationDelivery: "delivery-1",
	} {
		subjects, err := store.SubjectsOf(ctx, relation, []string{string(id)})
		require.NoError(t, err)
		assert.Equal(t, want, subjects[string(id)], relation)
	}
	page, err := store.RelationPage(ctx, postgres.RelationCourier, string(id)[:len(id)-1], 1000)
	require.NoError(t, err)
	assert.Contains(t, page, postgres.RelationSubject{OrderID: string(id), SubjectID: "courier-2"})
	_, err = store.SubjectsOf(ctx, postgres.Relation("owner"), []string{string(id)})
	require.Error(t, err)
}

// AC-534, AC-535, AC-538: a released assignment keeps its time and stores the
// cleared courier (and delivery) as NULL, so the relations are absent.
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

	remember(order.CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: at}, "zed-assigned")
	remember(order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: at.Add(time.Minute)}, "zed-unassigned")
	stored, found, err := store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	require.True(t, found, "a released assignment still orders later events")
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: at.Add(time.Minute)}, stored)
	_, ok := subject(postgres.RelationCourier)
	assert.False(t, ok, "the released courier is absent")
	delivery, ok := subject(postgres.RelationDelivery)
	assert.True(t, ok)
	assert.Equal(t, "delivery-1", delivery)
	revision, _, err := store.Revision(ctx, string(id))
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("zed-unassigned"), revision)

	remember(order.CourierAssignment{AssignedAt: at.Add(2 * time.Minute)}, "zed-cancelled")
	stored, found, err = store.CourierAssignment(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, order.CourierAssignment{AssignedAt: at.Add(2 * time.Minute)}, stored)
	_, ok = subject(postgres.RelationDelivery)
	assert.False(t, ok, "the cancelled delivery is absent")
}
