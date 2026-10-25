package order

import (
	"context"
	"time"
)

// CourierAssignment is the Courier Assignment of an Order as reported by
// delivery. CourierID is a user id: the courier is checked as user:<id>. After
// a release CourierID (and, for a cancelled Delivery, DeliveryID) is empty and
// AssignedAt and DeliveryVersion are those of the release: the stored
// assignment orders every later courier event of the Order.
type CourierAssignment struct {
	DeliveryID string
	CourierID  string
	AssignedAt time.Time
	// DeliveryVersion is the Delivery aggregate_version of the event that
	// recorded the assignment or the release. An Order has one Delivery and
	// every Delivery transaction increments its version, so the version
	// orders courier events; occurred_at (the start of the delivery
	// transaction) does not. Zero — recorded before versions were stored.
	DeliveryVersion uint64
}

// Supersedes reports whether the assignment may replace stored: an event of an
// older Delivery version than the stored assignment or release is out of
// order and changes nothing. An event of the same version is a repeat and is
// applied again idempotently.
func (a CourierAssignment) Supersedes(stored CourierAssignment) bool {
	return stored.DeliveryVersion <= a.DeliveryVersion
}

// Displaced returns the parts of previous that the assignment replaces: its
// courier and its delivery when they are set and differ. The same assignment
// displaces nothing.
func (a CourierAssignment) Displaced(previous CourierAssignment) CourierAssignment {
	var displaced CourierAssignment
	if previous.CourierID != "" && previous.CourierID != a.CourierID {
		displaced.CourierID = previous.CourierID
	}
	if previous.DeliveryID != "" && previous.DeliveryID != a.DeliveryID {
		displaced.DeliveryID = previous.DeliveryID
	}
	return displaced
}

// CourierRelease is the end of a Courier Assignment reported by delivery: an
// unassignment without a replacement, or a cancelled Delivery, which releases
// whichever courier it has.
type CourierRelease struct {
	DeliveryID        string
	CourierID         string
	DeliveryCancelled bool
	OccurredAt        time.Time
	DeliveryVersion   uint64
}

// Kept is the assignment that remains after the release: without courier,
// without delivery when it was cancelled, at the time and Delivery version of
// the release, so an older assignment handled afterwards does not supersede
// it. A release that arrives before any assignment is remembered ahead as it.
func (r CourierRelease) Kept() CourierAssignment {
	kept := CourierAssignment{DeliveryID: r.DeliveryID, AssignedAt: r.OccurredAt, DeliveryVersion: r.DeliveryVersion}
	if r.DeliveryCancelled {
		kept.DeliveryID = ""
	}
	return kept
}

// Release applies a release to the stored assignment of the same Delivery that
// is not newer (by Delivery version). After it the Delivery has no courier, so
// whichever courier is stored is released, even if the event names another
// one: the assignment of that one is older and is skipped when it arrives. ok
// is false for a stale release, which changes nothing.
func (a CourierAssignment) Release(release CourierRelease) (CourierAssignment, bool) {
	if (a.DeliveryID != "" && a.DeliveryID != release.DeliveryID) || a.DeliveryVersion > release.DeliveryVersion {
		return CourierAssignment{}, false
	}
	return release.Kept(), true
}

// HoldsRelationsOf reports whether releasing the assignment removes
// relationships: its courier, or its delivery when the Delivery is cancelled.
func (a CourierAssignment) HoldsRelationsOf(release CourierRelease) bool {
	return a.CourierID != "" || (release.DeliveryCancelled && a.DeliveryID != "")
}

// CourierAssignments is the persistence contract of the Courier Assignment of
// an Order. Both methods participate in the active Unit of Work from ctx;
// CourierAssignment locks the assignment until its end, so changes of one
// Order apply one after another. revision is the opaque revision of the
// relationship write that made the assignment visible; empty keeps the stored
// one.
type CourierAssignments interface {
	CourierAssignment(ctx context.Context, orderID ID) (CourierAssignment, bool, error)
	RememberCourierAssignment(ctx context.Context, orderID ID, assignment CourierAssignment, revision string) error
}
