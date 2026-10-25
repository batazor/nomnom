package recordcourierrelease_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recordcourierrelease"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

var assignedAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

type world struct {
	store      *memory.Store
	authorizer *memory.Authorizer
	workflow   *memory.Workflow
	assign     recordcourierassignment.Handler
	release    recordcourierrelease.Handler
}

// newWorld has order-1 with courier-1 assigned on delivery-1 at assignedAt.
func newWorld(t *testing.T) world {
	t.Helper()
	w := world{store: memory.NewStore(), authorizer: memory.NewAuthorizer(), workflow: memory.NewWorkflow()}
	require.NoError(t, w.store.Add(context.Background(), ordertest.New(t, "order-1")))
	w.assign = recordcourierassignment.NewHandler(w.store, w.store, w.store, w.workflow, w.store, w.authorizer)
	w.release = recordcourierrelease.NewHandler(w.store, w.store, w.store, w.authorizer)
	_, err := w.assign.Handle(context.Background(), recordcourierassignment.Input{
		EventID: "assigned-1", OrderID: "order-1", OccurredAt: assignedAt, DeliveryID: "delivery-1", CourierID: "courier-1",
	})
	require.NoError(t, err)
	require.True(t, w.courierCanView(t, "courier-1"))
	return w
}

func (w world) courierCanView(t *testing.T, courierID string) bool {
	t.Helper()
	return w.authorizer.Check(context.Background(), application.AccessCheck{
		Subject:    application.Subject{Kind: application.SubjectUser, ID: courierID},
		Permission: "view",
		Resource:   application.Resource{Type: "order", ID: "order-1"},
	}) == nil
}

func (w world) stored(t *testing.T) recordcourierassignment.Assignment {
	t.Helper()
	stored, found, err := w.store.CourierAssignment(context.Background(), "order-1")
	require.NoError(t, err)
	require.True(t, found)
	return stored
}

func unassigned(eventID, deliveryID, courierID string, at time.Time) recordcourierrelease.Input {
	return recordcourierrelease.Input{EventID: eventID, OrderID: "order-1", OccurredAt: at, DeliveryID: deliveryID, CourierID: courierID}
}

func cancelled(eventID, deliveryID string, at time.Time) recordcourierrelease.Input {
	return recordcourierrelease.Input{EventID: eventID, OrderID: "order-1", OccurredAt: at, DeliveryID: deliveryID, DeliveryCancelled: true}
}

// AC-534: the unassignment revokes the courier, keeps the delivery, stores the
// revision and leaves the workflow alone.
func TestUnassignmentRevokesCourier(t *testing.T) {
	w := newWorld(t)
	releasedAt := assignedAt.Add(time.Minute)

	result, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", releasedAt))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.False(t, w.courierCanView(t, "courier-1"))
	assert.Equal(t, recordcourierassignment.Assignment{DeliveryID: "delivery-1", AssignedAt: releasedAt}, w.stored(t))
	revision, _, err := w.store.Revision(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("memory:order-1:courier:2"), revision)
	assert.Len(t, w.workflow.Notifications(), 1, "only the assignment notified the workflow")
}

// AC-535: the cancellation clears the courier and the delivery.
func TestCancellationReleasesCourierAndDelivery(t *testing.T) {
	w := newWorld(t)
	cancelledAt := assignedAt.Add(time.Minute)

	result, err := w.release.Handle(context.Background(), cancelled("event-1", "delivery-1", cancelledAt))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.False(t, w.courierCanView(t, "courier-1"))
	assert.Equal(t, recordcourierassignment.Assignment{AssignedAt: cancelledAt}, w.stored(t))
}

// AC-536: an event that does not name the stored assignment changes nothing.
func TestEventOfAnotherAssignmentIsIgnored(t *testing.T) {
	tests := []struct {
		name string
		in   recordcourierrelease.Input
	}{
		{name: "another courier", in: unassigned("event-1", "delivery-1", "courier-2", assignedAt.Add(time.Minute))},
		{name: "another delivery", in: unassigned("event-1", "delivery-2", "courier-1", assignedAt.Add(time.Minute))},
		{name: "older than the assignment", in: unassigned("event-1", "delivery-1", "courier-1", assignedAt.Add(-time.Minute))},
		{name: "cancellation of another delivery", in: cancelled("event-1", "delivery-2", assignedAt.Add(time.Minute))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)

			result, err := w.release.Handle(context.Background(), tt.in)

			require.NoError(t, err)
			assert.Equal(t, recordcourierrelease.OutcomeIgnoredStale, result.Outcome)
			assert.True(t, w.courierCanView(t, "courier-1"))
			assert.Equal(t, 1, w.authorizer.CourierWrites())
			assert.Equal(t, recordcourierassignment.Assignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt}, w.stored(t))
			assert.Equal(t, 2, w.store.InboxSize(), "the event is recorded in the Inbox")
		})
	}
}

// AC-536: an assignment older than the release, delivered after it, does not
// grant access again; a newer one does.
func TestOlderAssignmentAfterReleaseIsSkipped(t *testing.T) {
	w := newWorld(t)
	_, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", assignedAt.Add(2*time.Minute)))
	require.NoError(t, err)

	_, err = w.assign.Handle(context.Background(), recordcourierassignment.Input{
		EventID: "assigned-2", OrderID: "order-1", OccurredAt: assignedAt.Add(time.Minute), DeliveryID: "delivery-1", CourierID: "courier-2",
	})
	require.NoError(t, err)
	assert.False(t, w.courierCanView(t, "courier-2"))

	_, err = w.assign.Handle(context.Background(), recordcourierassignment.Input{
		EventID: "assigned-3", OrderID: "order-1", OccurredAt: assignedAt.Add(3 * time.Minute), DeliveryID: "delivery-1", CourierID: "courier-3",
	})
	require.NoError(t, err)
	assert.True(t, w.courierCanView(t, "courier-3"))
	assert.False(t, w.courierCanView(t, "courier-1"))
}

func TestRedeliveryIsDuplicate(t *testing.T) {
	w := newWorld(t)
	in := unassigned("event-1", "delivery-1", "courier-1", assignedAt.Add(time.Minute))

	_, err := w.release.Handle(context.Background(), in)
	require.NoError(t, err)
	result, err := w.release.Handle(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeDuplicate, result.Outcome)
	assert.Equal(t, 2, w.authorizer.CourierWrites())
}

// AC-537: a failed relationship delete rolls the Inbox record back.
func TestRelationshipFailureRollsBack(t *testing.T) {
	w := newWorld(t)
	w.authorizer.FailWrites(errors.New("spicedb unavailable"))
	in := unassigned("event-1", "delivery-1", "courier-1", assignedAt.Add(time.Minute))

	_, err := w.release.Handle(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, 1, w.store.InboxSize())
	assert.Equal(t, "courier-1", w.stored(t).CourierID)

	w.authorizer.FailWrites(nil)
	result, err := w.release.Handle(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.False(t, w.courierCanView(t, "courier-1"))
}

func TestInvalidInput(t *testing.T) {
	w := newWorld(t)
	for name, in := range map[string]recordcourierrelease.Input{
		"no event id":           {OrderID: "order-1", OccurredAt: assignedAt, DeliveryID: "delivery-1", CourierID: "courier-1"},
		"no occurred_at":        {EventID: "event-1", OrderID: "order-1", DeliveryID: "delivery-1", CourierID: "courier-1"},
		"no delivery":           {EventID: "event-1", OrderID: "order-1", OccurredAt: assignedAt, CourierID: "courier-1"},
		"unassigned no courier": {EventID: "event-1", OrderID: "order-1", OccurredAt: assignedAt, DeliveryID: "delivery-1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := w.release.Handle(context.Background(), in)
			require.ErrorIs(t, err, application.ErrInvalidInput)
		})
	}
}
