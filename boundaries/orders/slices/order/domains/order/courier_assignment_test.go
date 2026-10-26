package order

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var assignedAt = time.Date(2026, 5, 1, 12, 30, 0, 0, time.UTC)

// AC-636: the domain model decides that an older assignment event is stale,
// by the Delivery version and not by occurred_at.
func TestCourierAssignmentSupersedesOnlyNotOlderVersions(t *testing.T) {
	stored := CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt, DeliveryVersion: 3}

	assert.True(t, CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-2", DeliveryVersion: 4}.Supersedes(stored))
	assert.True(t, CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", DeliveryVersion: 3}.Supersedes(stored), "a redelivery repeats the same assignment")
	assert.False(t, CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-0", AssignedAt: assignedAt.Add(time.Hour), DeliveryVersion: 2}.Supersedes(stored),
		"a later occurred_at of an older version is still stale")

	released := CourierRelease{DeliveryID: "delivery-1", DeliveryVersion: 5}.Kept()
	assert.False(t, stored.Supersedes(released), "a release keeps its version and orders older assignments")
}

// AC-519: a reassignment within the same Delivery changes no relationship; a
// change of Delivery displaces the previous one.
func TestCourierAssignmentConcernsOnlyTheDeliveryRelation(t *testing.T) {
	next := CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-2"}

	assert.True(t, next.LinksDelivery(CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1"}), "reassignment within the Delivery")
	assert.True(t, next.LinksDelivery(next), "redelivery")
	assert.False(t, next.LinksDelivery(CourierAssignment{DeliveryID: "delivery-1"}), "a release may precede the link")
	assert.False(t, next.LinksDelivery(CourierAssignment{DeliveryID: "delivery-0", CourierID: "courier-1"}))
	assert.False(t, next.LinksDelivery(CourierAssignment{}))

}

func TestCourierAssignmentReleaseKeepsItsTimeAndVersion(t *testing.T) {
	stored := CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt, DeliveryVersion: 2}
	releasedAt := assignedAt.Add(time.Minute)

	kept, ok := stored.Release(CourierRelease{DeliveryID: "delivery-1", CourierID: "courier-1", OccurredAt: releasedAt, DeliveryVersion: 3})
	assert.True(t, ok)
	assert.Equal(t, CourierAssignment{DeliveryID: "delivery-1", AssignedAt: releasedAt, DeliveryVersion: 3}, kept)

	kept, ok = stored.Release(CourierRelease{DeliveryID: "delivery-1", CourierID: "courier-9", OccurredAt: releasedAt, DeliveryVersion: 3})
	assert.True(t, ok, "the Delivery has no courier afterwards, whichever one the event names")
	assert.Equal(t, CourierAssignment{DeliveryID: "delivery-1", AssignedAt: releasedAt, DeliveryVersion: 3}, kept)

	kept, ok = stored.Release(CourierRelease{DeliveryID: "delivery-1", DeliveryCancelled: true, OccurredAt: releasedAt, DeliveryVersion: 3})
	assert.True(t, ok, "a cancellation releases whichever courier the Delivery has")
	assert.Equal(t, CourierAssignment{AssignedAt: releasedAt, DeliveryVersion: 3}, kept)
}

func TestCourierAssignmentIgnoresStaleRelease(t *testing.T) {
	stored := CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1", AssignedAt: assignedAt, DeliveryVersion: 3}
	for name, release := range map[string]CourierRelease{
		"another delivery": {DeliveryID: "delivery-2", CourierID: "courier-1", DeliveryVersion: 4},
		"older release":    {DeliveryID: "delivery-1", CourierID: "courier-1", DeliveryVersion: 2},
		"older cancel":     {DeliveryID: "delivery-1", DeliveryCancelled: true, DeliveryVersion: 2},
	} {
		t.Run(name, func(t *testing.T) {
			kept, ok := stored.Release(release)
			assert.False(t, ok)
			assert.Zero(t, kept)
		})
	}
}

func TestCourierAssignmentHoldsRelations(t *testing.T) {
	cancel := CourierRelease{DeliveryID: "delivery-1", DeliveryCancelled: true}
	unassign := CourierRelease{DeliveryID: "delivery-1", CourierID: "courier-1"}

	assert.False(t, CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1"}.HoldsRelationsOf(unassign), "delivery revokes the courier")
	assert.True(t, CourierAssignment{DeliveryID: "delivery-1", CourierID: "courier-1"}.HoldsRelationsOf(cancel))
	assert.True(t, CourierAssignment{DeliveryID: "delivery-1"}.HoldsRelationsOf(cancel), "the delivery relation of a cancelled Delivery")
	assert.False(t, CourierAssignment{}.HoldsRelationsOf(cancel))
}
