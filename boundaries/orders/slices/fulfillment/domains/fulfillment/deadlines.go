package fulfillment

import "time"

// Expiry is the reaction of the process to a passed deadline.
type Expiry int

const (
	// ExpiryNone: no active deadline has passed.
	ExpiryNone Expiry = iota
	// ExpiryReplan: the courier assignment is due for ReplanDelivery.
	ExpiryReplan
	// ExpiryEscalation: replans are exhausted, the process waits for an operator decision.
	ExpiryEscalation
	// ExpiryFailure: the deadline chose a negative outcome.
	ExpiryFailure
)

// NextDeadline is the nearest active deadline; zero means the process waits
// for a fact without a deadline.
func (p *Process) NextDeadline() time.Time {
	var next time.Time
	consider := func(active bool, deadline time.Time) {
		if active && !deadline.IsZero() && (next.IsZero() || deadline.Before(next)) {
			next = deadline
		}
	}
	consider(p.restaurantRequested && !p.accepted, p.acceptanceDeadline)
	consider(p.awaitingAssignment(), p.assignmentDeadline)
	consider(p.escalated, p.operatorDeadline)
	consider(p.accepted && !p.ready, p.preparationDeadline)
	consider(p.ready && p.assigned && !p.pickedUp, p.pickupDeadline)
	consider(p.pickedUp && !p.done, p.deliveryDeadline)
	return next
}

// Expire turns the deadlines passed at now into business branches.
func (p *Process) Expire(now time.Time) Expiry {
	passed := func(deadline time.Time) bool { return !deadline.IsZero() && !deadline.After(now) }
	switch {
	case p.restaurantRequested && !p.accepted && passed(p.acceptanceDeadline):
		p.fail(OutcomeFailure, ReasonRestaurantAcceptanceTimeout)
	case p.escalated && passed(p.operatorDeadline):
		p.fail(OutcomeFailure, ReasonCourierAssignmentExhausted)
	case p.awaitingAssignment() && passed(p.assignmentDeadline):
		if p.replans < p.policy.MaxReplans {
			p.replanDue = true
			return ExpiryReplan
		}
		p.escalated = true
		p.operatorDeadline = now.Add(p.policy.OperatorDecisionTimeout)
		return ExpiryEscalation
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

func (p *Process) awaitingAssignment() bool {
	return p.deliveryCreated && !p.assigned && !p.escalated && !p.replanDue
}
