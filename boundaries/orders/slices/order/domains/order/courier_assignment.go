package order

import (
	"context"
	"time"
)

// CourierAssignment is the Courier Assignment of an Order as reported by
// delivery. CourierID is a user id: the courier is checked as user:<id>. After
// a release CourierID (and, for a cancelled Delivery, DeliveryID) is empty and
// AssignedAt is the time of the release: the stored assignment orders every
// later courier event of the Order.
type CourierAssignment struct {
	DeliveryID string
	CourierID  string
	AssignedAt time.Time
}

// Supersedes reports whether the assignment may replace stored: an event older
// than the stored assignment or release is out of order and changes nothing.
func (a CourierAssignment) Supersedes(stored CourierAssignment) bool {
	return !stored.AssignedAt.After(a.AssignedAt)
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
}

// Release applies a release to the assignment it names: the same delivery, the
// same courier for an unassignment, and not older than the assignment. It
// returns the assignment to keep: without courier, without delivery when it
// was cancelled, at the time of the release, so an older assignment
// redelivered afterwards does not supersede it. ok is false for a stale
// release, which changes nothing.
func (a CourierAssignment) Release(release CourierRelease) (CourierAssignment, bool) {
	switch {
	case a.DeliveryID != release.DeliveryID,
		a.AssignedAt.After(release.OccurredAt),
		!release.DeliveryCancelled && a.CourierID != release.CourierID:
		return CourierAssignment{}, false
	}
	kept := CourierAssignment{DeliveryID: a.DeliveryID, AssignedAt: release.OccurredAt}
	if release.DeliveryCancelled {
		kept.DeliveryID = ""
	}
	return kept, true
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
