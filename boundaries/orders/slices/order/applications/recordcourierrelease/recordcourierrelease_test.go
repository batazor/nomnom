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

var (
	assignedAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)
	releasedAt = assignedAt.Add(time.Minute)
)

// assignedVersion is the Delivery version of the assignment of newWorld.
const assignedVersion = 2

type world struct {
	store      *memory.Store
	authorizer *memory.Authorizer
	workflow   *memory.Workflow
	assign     recordcourierassignment.Handler
	release    recordcourierrelease.Handler
}

// newWorld has order-1 with courier-1 assigned on delivery-1 at assignedAt
// (Delivery version assignedVersion).
func newWorld(t *testing.T) world {
	t.Helper()
	w := newUnassignedWorld(t)
	w.assignCourier(t, "assigned-1", "courier-1", assignedVersion, assignedAt)
	require.True(t, w.courierCanView(t, "courier-1"))
	return w
}

// newUnassignedWorld has order-1 without a stored Courier Assignment.
func newUnassignedWorld(t *testing.T) world {
	t.Helper()
	w := world{store: memory.NewStore(), authorizer: memory.NewAuthorizer(), workflow: memory.NewWorkflow()}
	require.NoError(t, w.store.Add(context.Background(), ordertest.New(t, "order-1")))
	w.assign = recordcourierassignment.NewHandler(w.store, w.store, w.store, w.workflow, w.store, w.authorizer)
	w.release = recordcourierrelease.NewHandler(w.store, w.store, w.store, w.authorizer)
	return w
}

func (w world) assignCourier(t *testing.T, eventID, courierID string, version uint64, at time.Time) {
	t.Helper()
	_, err := w.assign.Handle(context.Background(), recordcourierassignment.Input{
		EventID: eventID, OrderID: "order-1", OccurredAt: at, DeliveryVersion: version, DeliveryID: "delivery-1", CourierID: courierID,
	})
	require.NoError(t, err)
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

// unassigned is an unassignment at releasedAt of Delivery version version.
func unassigned(eventID, deliveryID, courierID string, version uint64) recordcourierrelease.Input {
	return recordcourierrelease.Input{
		EventID: eventID, OrderID: "order-1", OccurredAt: releasedAt, DeliveryVersion: version, DeliveryID: deliveryID, CourierID: courierID,
	}
}

// cancelled is a cancellation at releasedAt of Delivery version version.
func cancelled(eventID, deliveryID string, version uint64) recordcourierrelease.Input {
	return recordcourierrelease.Input{
		EventID: eventID, OrderID: "order-1", OccurredAt: releasedAt, DeliveryVersion: version, DeliveryID: deliveryID, DeliveryCancelled: true,
	}
}

// AC-534: the unassignment revokes the courier, keeps the delivery, stores the
// revision and leaves the workflow alone.
func TestUnassignmentRevokesCourier(t *testing.T) {
	w := newWorld(t)

	result, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 3))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.False(t, w.courierCanView(t, "courier-1"))
	assert.Equal(t, recordcourierassignment.Assignment{DeliveryID: "delivery-1", AssignedAt: releasedAt, DeliveryVersion: 3}, w.stored(t))
	revision, _, err := w.store.Revision(context.Background(), "order-1")
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("memory:order-1:courier:2"), revision)
	assert.Len(t, w.workflow.Notifications(), 1, "only the assignment notified the workflow")
}

// AC-535: the cancellation clears the courier and the delivery.
func TestCancellationReleasesCourierAndDelivery(t *testing.T) {
	w := newWorld(t)

	result, err := w.release.Handle(context.Background(), cancelled("event-1", "delivery-1", 3))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.False(t, w.courierCanView(t, "courier-1"))
	assert.Equal(t, recordcourierassignment.Assignment{AssignedAt: releasedAt, DeliveryVersion: 3}, w.stored(t))
}

// AC-536: an event of another Delivery or of an older Delivery version than
// the stored assignment changes nothing.
func TestEventOfAnotherAssignmentIsIgnored(t *testing.T) {
	older := unassigned("event-1", "delivery-1", "courier-1", assignedVersion-1)
	// occurred_at is later than the assignment; the version still orders it before.
	older.OccurredAt = assignedAt.Add(time.Hour)
	tests := []struct {
		name string
		in   recordcourierrelease.Input
	}{
		{name: "another delivery", in: unassigned("event-1", "delivery-2", "courier-1", 3)},
		{name: "older version than the assignment", in: older},
		{name: "cancellation of another delivery", in: cancelled("event-1", "delivery-2", 3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)

			result, err := w.release.Handle(context.Background(), tt.in)

			require.NoError(t, err)
			assert.Equal(t, recordcourierrelease.OutcomeIgnoredStale, result.Outcome)
			assert.True(t, w.courierCanView(t, "courier-1"))
			assert.Equal(t, 1, w.authorizer.CourierWrites())
			assert.Equal(t, recordcourierassignment.Assignment{
				DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt, DeliveryVersion: assignedVersion,
			}, w.stored(t))
			assert.Equal(t, 2, w.store.InboxSize(), "the event is recorded in the Inbox")
		})
	}
}

// AC-536: an assignment of an older Delivery version than the release,
// delivered after it, does not grant access again; a newer one does.
func TestOlderAssignmentAfterReleaseIsSkipped(t *testing.T) {
	w := newWorld(t)
	_, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 4))
	require.NoError(t, err)

	// occurred_at is later than the release; the version orders it before.
	w.assignCourier(t, "assigned-2", "courier-2", 3, releasedAt.Add(time.Hour))
	assert.False(t, w.courierCanView(t, "courier-2"))

	w.assignCourier(t, "assigned-3", "courier-3", 5, releasedAt)
	assert.True(t, w.courierCanView(t, "courier-3"))
	assert.False(t, w.courierCanView(t, "courier-1"))
}

// AC-669: a release of a newer Delivery version than the stored assignment is
// applied even if occurred_at is older: occurred_at is the start of the
// delivery transaction and does not order commits.
func TestVersionOrdersReleaseNotOccurredAt(t *testing.T) {
	w := newWorld(t)
	in := unassigned("event-1", "delivery-1", "courier-1", assignedVersion+1)
	in.OccurredAt = assignedAt.Add(-time.Minute)

	result, err := w.release.Handle(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.False(t, w.courierCanView(t, "courier-1"))
}

// AC-670: a newer unassignment that names another courier (its assignment
// has not been handled yet) leaves the Delivery without a courier: the stored
// courier is released and the late assignment of the named one is skipped.
func TestNewerUnassignmentOfAnotherCourierReleasesStoredOne(t *testing.T) {
	w := newWorld(t)

	result, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-2", 4))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.False(t, w.courierCanView(t, "courier-1"))
	w.assignCourier(t, "assigned-2", "courier-2", 3, releasedAt)
	assert.False(t, w.courierCanView(t, "courier-2"), "the assignment the unassignment ended is skipped")
	assert.Equal(t, recordcourierassignment.Assignment{DeliveryID: "delivery-1", AssignedAt: releasedAt, DeliveryVersion: 4}, w.stored(t))
}

// AC-670: a release handled before any assignment is remembered with its
// Delivery version, so the older assignment handled afterwards is skipped.
func TestReleaseBeforeAssignmentIsRemembered(t *testing.T) {
	for name, in := range map[string]recordcourierrelease.Input{
		"unassignment": unassigned("event-1", "delivery-1", "courier-1", 3),
		"cancellation": cancelled("event-1", "delivery-1", 3),
	} {
		t.Run(name, func(t *testing.T) {
			w := newUnassignedWorld(t)

			result, err := w.release.Handle(context.Background(), in)

			require.NoError(t, err)
			assert.Equal(t, recordcourierrelease.OutcomeRememberedAhead, result.Outcome)
			assert.Equal(t, 0, w.authorizer.CourierWrites(), "nothing to delete")
			remembered := w.stored(t)
			assert.Equal(t, uint64(3), remembered.DeliveryVersion)
			assert.Empty(t, remembered.CourierID)

			w.assignCourier(t, "assigned-1", "courier-1", assignedVersion, assignedAt)
			assert.False(t, w.courierCanView(t, "courier-1"), "the older assignment is skipped")
			assert.Equal(t, 0, w.authorizer.CourierWrites())
			assert.Equal(t, remembered, w.stored(t))
		})
	}
}

func TestRedeliveryIsDuplicate(t *testing.T) {
	w := newWorld(t)
	in := unassigned("event-1", "delivery-1", "courier-1", 3)

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
	in := unassigned("event-1", "delivery-1", "courier-1", 3)

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
