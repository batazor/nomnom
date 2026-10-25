package order

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var assignedAt = time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)

// AC-636: the domain model decides that an older assignment event is stale.
func TestCourierAssignmentSupersedesOnlyNotOlderAssignments(t *testing.T) {
	stored := CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt}

	assert.True(t, CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-2", AssignedAt: assignedAt.Add(time.Minute)}.Supersedes(stored))
	assert.True(t, CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt}.Supersedes(stored), "a redelivery repeats the same assignment")
	assert.False(t, CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-0", AssignedAt: assignedAt.Add(-time.Minute)}.Supersedes(stored))

	released := CourierAssignment{DeliveryID: "delivery-1", AssignedAt: assignedAt.Add(time.Hour)}
	assert.False(t, stored.Supersedes(released), "a release keeps its time and orders older assignments")
}

func TestCourierAssignmentDisplacesOnlyDifferentParts(t *testing.T) {
	next := CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-2"}

	assert.Equal(t, CourierAssignment{CourierID: "courier-1"}, next.Displaced(CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1"}))
	assert.Equal(t, CourierAssignment{DeliveryID: "delivery-0", CourierID: "courier-1"}, next.Displaced(CourierAssignment{DeliveryID: "delivery-0", CourierID: "courier-1"}))
	assert.Zero(t, next.Displaced(next), "the same assignment displaces nothing")
	assert.Zero(t, next.Displaced(CourierAssignment{DeliveryID: "delivery-1"}), "a released assignment has no courier to displace")
	assert.Zero(t, next.Displaced(CourierAssignment{}))
}

func TestCourierAssignmentReleaseKeepsItsTime(t *testing.T) {
	stored := CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt}
	releasedAt := assignedAt.Add(time.Minute)

	kept, ok := stored.Release(CourierRelease{DeliveryID: "delivery-1", CourierID: "courier-1", OccurredAt: releasedAt})
	assert.True(t, ok)
	assert.Equal(t, CourierAssignment{DeliveryID: "delivery-1", AssignedAt: releasedAt}, kept)

	kept, ok = stored.Release(CourierRelease{DeliveryID: "delivery-1", DeliveryCancelled: true, OccurredAt: releasedAt})
	assert.True(t, ok, "a cancellation releases whichever courier the Delivery has")
	assert.Equal(t, CourierAssignment{AssignedAt: releasedAt}, kept)
}

func TestCourierAssignmentIgnoresStaleRelease(t *testing.T) {
	stored := CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt}
	for name, release := range map[string]CourierRelease{
		"another delivery": {DeliveryID: "delivery-2", CourierID: "courier-1", OccurredAt: assignedAt.Add(time.Minute)},
		"another courier":  {DeliveryID: "delivery-1", CourierID: "courier-2", OccurredAt: assignedAt.Add(time.Minute)},
		"older release":    {DeliveryID: "delivery-1", CourierID: "courier-1", OccurredAt: assignedAt.Add(-time.Minute)},
		"older cancel":     {DeliveryID: "delivery-1", DeliveryCancelled: true, OccurredAt: assignedAt.Add(-time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			kept, ok := stored.Release(release)
			assert.False(t, ok)
			assert.Zero(t, kept)
		})
	}
}
