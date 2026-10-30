package fulfillment

import "time"

// Expiry is the reaction of the process to a passed deadline.
type Expiry int

const (
	// ExpiryNone: no active deadline has passed.
	ExpiryNone Expiry = iota
	// ExpiryFailure: the deadline chose a negative outcome.
	ExpiryFailure
	// ExpiryScheduledStep: a moment of the plan of a Scheduled Order passed,
	// its step is due (FR-678).
	ExpiryScheduledStep
)

// NextDeadline is the nearest active deadline; zero means the process waits
// for a signal without a deadline.
func (p *Process) NextDeadline() time.Time {
	var next time.Time
	consider := func(active bool, deadline time.Time) {
		if active && !deadline.IsZero() && (next.IsZero() || deadline.Before(next)) {
			next = deadline
		}
	}
	consider(p.restaurantRequested && !p.accepted, p.acceptanceDeadline)
	consider(p.accepted && !p.ready, p.preparationDeadline)
	consider(p.ready && p.assigned && !p.pickedUp, p.pickupDeadline)
	consider(p.pickedUp && !p.done, p.deliveryDeadline)
	if s := p.schedule; s != nil && p.accepted {
		// Timers of the stage scheduled: the replan checkpoint, the start of
		// the kitchen and the courier request.
		consider(!s.replanned && !s.replanDue, s.plan.ReplanAt)
		consider(!s.queued && !s.startDue, s.plan.StartAt)
		consider(!p.deliveryCreated && !s.courierDue, s.plan.CourierRequestAt)
	}
	return next
}

// Expire turns the deadlines passed at now into business branches.
func (p *Process) Expire(now time.Time) Expiry {
	passed := func(deadline time.Time) bool { return !deadline.IsZero() && !deadline.After(now) }
	if p.dueScheduledSteps(passed) {
		return ExpiryScheduledStep
	}
	switch {
	case p.restaurantRequested && !p.accepted && passed(p.acceptanceDeadline):
		p.fail(OutcomeFailure, ReasonRestaurantAcceptanceTimeout)
	case p.accepted && !p.ready && passed(p.preparationDeadline):
		p.fail(OutcomeFailure, ReasonPreparationTimeout)
	case p.ready && p.assigned && !p.pickedUp && passed(p.pickupDeadline):
		p.fail(OutcomeFailure, ReasonPickupTimeout)
	case p.pickedUp && !p.done && passed(p.deliveryDeadline):
		p.fail(OutcomeFailure, ReasonDeliveryTimeout)
	default:
		return ExpiryNone
	}
	return ExpiryFailure
}
