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
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

var (
	assignedAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)
	releasedAt = assignedAt.Add(time.Minute)
)

// assignedVersion is the Delivery version of the assignment of newWorld.
const assignedVersion = 2

// assignedRevision is the revision of the order#delivery write of newWorld.
const assignedRevision = application.AuthorizationRevision("memory:order-1:delivery:1")

type world struct {
	store      *memory.Store
	authorizer *memory.Authorizer
	workflow   *memory.Workflow
	assign     recordcourierassignment.Handler
	release    recordcourierrelease.Handler
}

// newWorld has order-1 with courier-1 assigned on delivery-1 at assignedAt
// (Delivery version assignedVersion); order#delivery links delivery-1.
func newWorld(t *testing.T) world {
	t.Helper()
	w := newUnassignedWorld(t)
	w.assignCourier(t, "assigned-1", "courier-1", assignedVersion, assignedAt)
	require.Equal(t, "delivery-1", w.linkedDelivery())
	require.Equal(t, 1, w.authorizer.DeliveryWrites())
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

func (w world) linkedDelivery() string {
	deliveryID, _ := w.authorizer.DeliveryOf("order-1")
	return deliveryID
}

func (w world) stored(t *testing.T) order.CourierAssignment {
	t.Helper()
	stored, found, err := w.store.CourierAssignment(context.Background(), "order-1")
	require.NoError(t, err)
	require.True(t, found)
	return stored
}

func (w world) revision(t *testing.T) application.AuthorizationRevision {
	t.Helper()
	revision, _, err := w.store.Revision(context.Background(), "order-1")
	require.NoError(t, err)
	return revision
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

// AC-534: the unassignment clears the courier and stores the version; there
// is no SpiceDB request, order#delivery stays and the workflow is left alone.
func TestUnassignmentClearsCourierOnly(t *testing.T) {
	w := newWorld(t)

	result, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 3))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.Equal(t, 1, w.authorizer.DeliveryWrites(), "no SpiceDB request")
	assert.Equal(t, "delivery-1", w.linkedDelivery())
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: releasedAt, DeliveryVersion: 3}, w.stored(t))
	assert.Equal(t, assignedRevision, w.revision(t))
	assert.Len(t, w.workflow.Notifications(), 1, "only the assignment notified the workflow")
}

// AC-535: the cancellation deletes order#delivery, clears the courier and the
// delivery and stores the revision.
func TestCancellationUnlinksDelivery(t *testing.T) {
	w := newWorld(t)

	result, err := w.release.Handle(context.Background(), cancelled("event-1", "delivery-1", 3))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.Empty(t, w.linkedDelivery())
	assert.Equal(t, order.CourierAssignment{AssignedAt: releasedAt, DeliveryVersion: 3}, w.stored(t))
	assert.Equal(t, application.AuthorizationRevision("memory:order-1:delivery:2"), w.revision(t))
}

// After an unassignment the cancellation of the same Delivery still unlinks it.
func TestCancellationAfterUnassignmentUnlinksDelivery(t *testing.T) {
	w := newWorld(t)
	_, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 3))
	require.NoError(t, err)

	result, err := w.release.Handle(context.Background(), cancelled("event-2", "delivery-1", 4))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.Empty(t, w.linkedDelivery())
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
			assert.Equal(t, "delivery-1", w.linkedDelivery())
			assert.Equal(t, 1, w.authorizer.DeliveryWrites())
			assert.Equal(t, order.CourierAssignment{
				DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt, DeliveryVersion: assignedVersion,
			}, w.stored(t))
			assert.Equal(t, 2, w.store.InboxSize(), "the event is recorded in the Inbox")
		})
	}
}

// AC-536: an assignment of an older Delivery version than the release,
// delivered after it, does not change the assignment; a newer one does.
func TestOlderAssignmentAfterReleaseIsSkipped(t *testing.T) {
	w := newWorld(t)
	_, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 4))
	require.NoError(t, err)

	// occurred_at is later than the release; the version orders it before.
	w.assignCourier(t, "assigned-2", "courier-2", 3, releasedAt.Add(time.Hour))
	assert.Empty(t, w.stored(t).CourierID)

	w.assignCourier(t, "assigned-3", "courier-3", 5, releasedAt)
	assert.Equal(t, "courier-3", w.stored(t).CourierID)
	assert.Equal(t, "delivery-1", w.linkedDelivery())
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
	assert.Empty(t, w.stored(t).CourierID)
}

// AC-670: a newer unassignment that names another courier (its assignment
// has not been handled yet) leaves the Delivery without a courier: the stored
// courier is cleared and the late assignment of the named one is skipped.
func TestNewerUnassignmentOfAnotherCourierReleasesStoredOne(t *testing.T) {
	w := newWorld(t)

	result, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-2", 4))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	w.assignCourier(t, "assigned-2", "courier-2", 3, releasedAt)
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: releasedAt, DeliveryVersion: 4}, w.stored(t),
		"the assignment the unassignment ended is skipped")
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
			assert.Equal(t, 0, w.authorizer.DeliveryWrites(), "nothing to delete")
			remembered := w.stored(t)
			assert.Equal(t, uint64(3), remembered.DeliveryVersion)
			assert.Empty(t, remembered.CourierID)

			w.assignCourier(t, "assigned-1", "courier-1", assignedVersion, assignedAt)
			assert.Equal(t, 0, w.authorizer.DeliveryWrites(), "the older assignment is skipped")
			assert.Empty(t, w.linkedDelivery())
			assert.Equal(t, remembered, w.stored(t))
		})
	}
}

// A newer assignment after a release remembered ahead links the Delivery: the
// release did not write order#delivery.
func TestNewerAssignmentAfterRememberedReleaseLinksDelivery(t *testing.T) {
	w := newUnassignedWorld(t)
	_, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 3))
	require.NoError(t, err)

	w.assignCourier(t, "assigned-2", "courier-2", 4, releasedAt)

	assert.Equal(t, "delivery-1", w.linkedDelivery())
	assert.Equal(t, "courier-2", w.stored(t).CourierID)
}

func TestRedeliveryIsDuplicate(t *testing.T) {
	w := newWorld(t)
	in := cancelled("event-1", "delivery-1", 3)

	_, err := w.release.Handle(context.Background(), in)
	require.NoError(t, err)
	result, err := w.release.Handle(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeDuplicate, result.Outcome)
	assert.Equal(t, 2, w.authorizer.DeliveryWrites())
}

// AC-537: a failed delete of order#delivery rolls the Inbox record back.
func TestRelationshipFailureRollsBack(t *testing.T) {
	w := newWorld(t)
	w.authorizer.FailWrites(errors.New("spicedb unavailable"))
	in := cancelled("event-1", "delivery-1", 3)

	_, err := w.release.Handle(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, 1, w.store.InboxSize())
	assert.Equal(t, "courier-1", w.stored(t).CourierID)

	w.authorizer.FailWrites(nil)
	result, err := w.release.Handle(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.Empty(t, w.linkedDelivery())
}

// AC-534: an unassignment needs no SpiceDB, so it succeeds while SpiceDB is
// unavailable.
func TestUnassignmentDoesNotNeedSpiceDB(t *testing.T) {
	w := newWorld(t)
	w.authorizer.FailWrites(errors.New("spicedb unavailable"))

	result, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 3))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
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
