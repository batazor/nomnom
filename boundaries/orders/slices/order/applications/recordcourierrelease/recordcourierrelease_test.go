package recordcourierrelease_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recordcourierrelease"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

var (
	assignedAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)
	releasedAt = assignedAt.Add(time.Minute)
)

// assignedVersion is the Delivery version of the assignment of newWorld.
const assignedVersion = 2

// assignedNotification is what the assignment of newWorld hands to the workflow.
var assignedNotification = fulfillment.Notification{
	OrderID: "order-1", EventID: "assigned-1", Kind: fulfillment.KindCourierAssigned, OccurredAt: assignedAt,
}

type world struct {
	store    *repositorytest.Store
	changes  *MockChanges
	workflow *MockNotifier
	assign   recordcourierassignment.Handler
	release  recordcourierrelease.Handler
}

// newWorld has order-1 with courier-1 assigned on delivery-1 at assignedAt
// (Delivery version assignedVersion); order#delivery links delivery-1. The
// test expects assignedNotification on workflow.
func newWorld(t *testing.T, workflow *MockNotifier) world {
	t.Helper()
	w := newUnassignedWorld(t, workflow)
	w.assignCourier(t, "assigned-1", "courier-1", assignedVersion, assignedAt)
	require.Equal(t, "delivery-1", w.linkedDelivery())
	require.Len(t, w.store.RelationshipChanges(), 1)
	return w
}

// newUnassignedWorld has order-1 without a stored Courier Assignment. The
// release records RelationshipsChanged through the changes mock; the
// assignment records it in the store.
func newUnassignedWorld(t *testing.T, workflow *MockNotifier) world {
	t.Helper()
	store := repositorytest.Open(t)
	w := world{store: store, changes: NewMockChanges(t), workflow: workflow}
	require.NoError(t, w.store.Add(context.Background(), ordertest.New(t, "order-1")))
	w.assign = recordcourierassignment.NewHandler(w.store, w.store, w.store, w.workflow, w.store, w.store)
	w.release = recordcourierrelease.NewHandler(w.store, w.store, w.store, w.changes)
	return w
}

// assignCourier handles the assignment on delivery-1; the test expects its
// notification on the workflow. The release handler never notifies it.
func (w world) assignCourier(t *testing.T, eventID, courierID string, version uint64, at time.Time) {
	t.Helper()
	_, err := w.assign.Handle(context.Background(), recordcourierassignment.Input{
		EventID: eventID, OrderID: "order-1", OccurredAt: at, DeliveryVersion: version, DeliveryID: "delivery-1", CourierID: courierID,
	})
	require.NoError(t, err)
}

// linkedDelivery is the stored Delivery: the subject of order#delivery that
// Sync Order Relationships writes (ADR 18).
func (w world) linkedDelivery() string {
	stored, _, _ := w.store.CourierAssignment(context.Background(), "order-1")
	return stored.DeliveryID
}

func (w world) stored(t *testing.T) order.CourierAssignment {
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

// AC-534: the unassignment clears the courier and stores the version; it
// records no RelationshipsChanged, order#delivery stays and the workflow is
// left alone.
func TestUnassignmentClearsCourierOnly(t *testing.T) {
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)

	result, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 3))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.Len(t, w.store.RelationshipChanges(), 1, "no RelationshipsChanged")
	assert.Equal(t, "delivery-1", w.linkedDelivery())
	// Only the assignment notified the workflow: the release has no expectation.
	assert.Equal(t, order.CourierAssignment{DeliveryID: "delivery-1", AssignedAt: releasedAt, DeliveryVersion: 3}, w.stored(t))
}

// AC-535: the cancellation clears the courier and the delivery and records
// RelationshipsChanged: the sync deletes order#delivery.
func TestCancellationUnlinksDelivery(t *testing.T) {
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)
	w.changes.EXPECT().RelationshipsChanged(mock.Anything, order.ID("order-1")).RunAndReturn(w.store.RelationshipsChanged).Once()

	result, err := w.release.Handle(context.Background(), cancelled("event-1", "delivery-1", 3))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.Empty(t, w.linkedDelivery())
	assert.Equal(t, order.CourierAssignment{AssignedAt: releasedAt, DeliveryVersion: 3}, w.stored(t))
	assert.Len(t, w.store.RelationshipChanges(), 2)
}

// After an unassignment the cancellation of the same Delivery still unlinks it.
func TestCancellationAfterUnassignmentUnlinksDelivery(t *testing.T) {
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)
	_, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 3))
	require.NoError(t, err)
	w.changes.EXPECT().RelationshipsChanged(mock.Anything, order.ID("order-1")).RunAndReturn(w.store.RelationshipsChanged).Once()

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
			workflow := NewMockNotifier(t)
			workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
			w := newWorld(t, workflow)

			result, err := w.release.Handle(context.Background(), tt.in)

			require.NoError(t, err)
			assert.Equal(t, recordcourierrelease.OutcomeIgnoredStale, result.Outcome)
			assert.Equal(t, "delivery-1", w.linkedDelivery())
			assert.Len(t, w.store.RelationshipChanges(), 1)
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
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)
	_, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 4))
	require.NoError(t, err)

	// occurred_at is later than the release; the version orders it before.
	w.workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "assigned-2", Kind: fulfillment.KindCourierAssigned, OccurredAt: releasedAt.Add(time.Hour),
		}).
		Return(nil).
		Once()
	w.assignCourier(t, "assigned-2", "courier-2", 3, releasedAt.Add(time.Hour))
	assert.Empty(t, w.stored(t).CourierID)

	w.workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "assigned-3", Kind: fulfillment.KindCourierAssigned, OccurredAt: releasedAt,
		}).
		Return(nil).
		Once()
	w.assignCourier(t, "assigned-3", "courier-3", 5, releasedAt)
	assert.Equal(t, "courier-3", w.stored(t).CourierID)
	assert.Equal(t, "delivery-1", w.linkedDelivery())
}

// AC-669: a release of a newer Delivery version than the stored assignment is
// applied even if occurred_at is older: occurred_at is the start of the
// delivery transaction and does not order commits.
func TestVersionOrdersReleaseNotOccurredAt(t *testing.T) {
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)
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
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)

	result, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-2", 4))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	w.workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "assigned-2", Kind: fulfillment.KindCourierAssigned, OccurredAt: releasedAt,
		}).
		Return(nil).
		Once()
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
			w := newUnassignedWorld(t, NewMockNotifier(t))

			result, err := w.release.Handle(context.Background(), in)

			require.NoError(t, err)
			assert.Equal(t, recordcourierrelease.OutcomeRememberedAhead, result.Outcome)
			assert.Empty(t, w.store.RelationshipChanges(), "nothing to unlink")
			remembered := w.stored(t)
			assert.Equal(t, uint64(3), remembered.DeliveryVersion)
			assert.Empty(t, remembered.CourierID)

			w.workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
			w.assignCourier(t, "assigned-1", "courier-1", assignedVersion, assignedAt)
			assert.Empty(t, w.store.RelationshipChanges(), "the older assignment is skipped")
			assert.Equal(t, remembered, w.stored(t), "the remembered release stays the state the sync reads")
		})
	}
}

// A newer assignment after a release remembered ahead links the Delivery: the
// release did not write order#delivery.
func TestNewerAssignmentAfterRememberedReleaseLinksDelivery(t *testing.T) {
	w := newUnassignedWorld(t, NewMockNotifier(t))
	_, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 3))
	require.NoError(t, err)

	w.workflow.EXPECT().
		Notify(mock.Anything, fulfillment.Notification{
			OrderID: "order-1", EventID: "assigned-2", Kind: fulfillment.KindCourierAssigned, OccurredAt: releasedAt,
		}).
		Return(nil).
		Once()
	w.assignCourier(t, "assigned-2", "courier-2", 4, releasedAt)

	assert.Equal(t, "delivery-1", w.linkedDelivery())
	assert.Equal(t, "courier-2", w.stored(t).CourierID)
}

func TestRedeliveryIsDuplicate(t *testing.T) {
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)
	in := cancelled("event-1", "delivery-1", 3)
	// The redelivery is a duplicate: only the first delivery records the change.
	w.changes.EXPECT().RelationshipsChanged(mock.Anything, order.ID("order-1")).RunAndReturn(w.store.RelationshipsChanged).Once()

	_, err := w.release.Handle(context.Background(), in)
	require.NoError(t, err)
	result, err := w.release.Handle(context.Background(), in)

	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeDuplicate, result.Outcome)
	assert.Len(t, w.store.RelationshipChanges(), 2)
}

// AC-537: RelationshipsChanged commits with the release: a failure rolls the
// Inbox record and the release back.
func TestRelationshipFailureRollsBack(t *testing.T) {
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)
	w.changes.EXPECT().RelationshipsChanged(mock.Anything, order.ID("order-1")).Return(errors.New("outbox unavailable")).Once()
	in := cancelled("event-1", "delivery-1", 3)

	_, err := w.release.Handle(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, 1, w.store.InboxSize())
	assert.Equal(t, "courier-1", w.stored(t).CourierID)

	w.changes.EXPECT().RelationshipsChanged(mock.Anything, order.ID("order-1")).RunAndReturn(w.store.RelationshipsChanged).Once()
	result, err := w.release.Handle(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
	assert.Empty(t, w.linkedDelivery())
}

// AC-534: an unassignment keeps order#delivery, so it records no
// RelationshipsChanged and succeeds while the Outbox write would fail.
func TestUnassignmentDoesNotNeedSpiceDB(t *testing.T) {
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)
	// The changes mock has no expectation: a RelationshipsChanged fails the test.

	result, err := w.release.Handle(context.Background(), unassigned("event-1", "delivery-1", "courier-1", 3))

	require.NoError(t, err)
	assert.Equal(t, recordcourierrelease.OutcomeReleased, result.Outcome)
}

func TestInvalidInput(t *testing.T) {
	workflow := NewMockNotifier(t)
	workflow.EXPECT().Notify(mock.Anything, assignedNotification).Return(nil).Once()
	w := newWorld(t, workflow)
	for name, in := range map[string]recordcourierrelease.Input{
		"no event id":           {OrderID: "order-1", OccurredAt: assignedAt, DeliveryID: "delivery-1", CourierID: "courier-1"},
		"no occurred_at":        {EventID: "event-1", OrderID: "order-1", DeliveryID: "delivery-1", CourierID: "courier-1"},
		"no delivery":           {EventID: "event-1", OrderID: "order-1", OccurredAt: assignedAt, CourierID: "courier-1"},
		"unassigned no courier": {EventID: "event-1", OrderID: "order-1", OccurredAt: assignedAt, DeliveryID: "delivery-1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := w.release.Handle(context.Background(), in)
			require.ErrorIs(t, err, usecase.ErrInvalidInput)
		})
	}
}
