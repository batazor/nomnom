// Package fulfillment is the domain model of the Order Fulfillment process:
// the journal of confirmed steps, facts of neighbouring contexts, replans,
// escalation and deadlines. Operations return decisions instead of performing
// side effects; the model reads no clock and generates no randomness, so the
// caller passes the time and executes the steps (ADR 5, FR-483).
package fulfillment

import "time"

type Outcome string

const (
	OutcomeCompleted    Outcome = "completed"
	OutcomeCancellation Outcome = "cancellation"
	OutcomeFailure      Outcome = "failure"
)

// Reason codes of the negative outcome of the Order.
const (
	ReasonPaymentDeclined             = "payment_declined"
	ReasonPaymentCaptureDeclined      = "payment_capture_declined"
	ReasonRestaurantRejected          = "restaurant_rejected"
	ReasonRestaurantAcceptanceTimeout = "restaurant_acceptance_timeout"
	ReasonPreparationFailed           = "preparation_failed"
	ReasonPreparationTimeout          = "preparation_timeout"
	ReasonCourierAssignmentExhausted  = "courier_assignment_exhausted"
	ReasonPickupTimeout               = "pickup_timeout"
	ReasonDeliveryFailed              = "delivery_failed"
	ReasonDeliveryTimeout             = "delivery_timeout"
	ReasonCustomerRequested           = "customer_requested"
	ReasonOperatorDecision            = "operator_decision"
)

// ReasonCodes lists every reason code the process can record on an Order.
func ReasonCodes() []string {
	return []string{
		ReasonPaymentDeclined, ReasonPaymentCaptureDeclined,
		ReasonRestaurantRejected, ReasonRestaurantAcceptanceTimeout,
		ReasonPreparationFailed, ReasonPreparationTimeout,
		ReasonCourierAssignmentExhausted, ReasonPickupTimeout,
		ReasonDeliveryFailed, ReasonDeliveryTimeout,
		ReasonCustomerRequested, ReasonOperatorDecision,
	}
}

type negative struct {
	outcome Outcome
	code    string
}

// Process is the state of the Order Fulfillment process of one Order.
type Process struct {
	policy Policy

	processed     map[string]bool
	cancellations map[string]bool
	final         Stage

	// Journal of confirmed steps and owner-side closures for the Saga.
	authorized          bool
	captured            bool
	restaurantRequested bool
	restaurantClosed    bool
	deliveryCreated     bool
	deliveryClosed      bool

	accepted bool
	ready    bool
	assigned bool
	pickedUp bool
	done     bool

	replans   int
	replanDue bool
	escalated bool

	acceptanceDeadline  time.Time
	assignmentDeadline  time.Time
	operatorDeadline    time.Time
	preparationDeadline time.Time
	preparationLimit    time.Time
	pickupDeadline      time.Time
	deliveryDeadline    time.Time

	failure *negative
}

// New starts the process with the deadlines fixed for its whole life.
func New(policy Policy) *Process {
	return &Process{
		policy:        policy,
		processed:     map[string]bool{},
		cancellations: map[string]bool{},
	}
}

// Failed reports that a negative outcome is chosen and the Saga must run.
func (p *Process) Failed() bool { return p.failure != nil && p.final == "" }

// Completed reports that the Order completed.
func (p *Process) Completed() bool { return p.final == StageCompleted }

// Replans is the number of confirmed ReplanDelivery steps.
func (p *Process) Replans() int { return p.replans }

// Outcome is the business outcome once chosen: completed, or the negative
// outcome with its reason code. It is empty while the route runs.
func (p *Process) Outcome() (Outcome, string) {
	switch {
	case p.final == StageCompleted:
		return OutcomeCompleted, ""
	case p.failure != nil:
		return p.failure.outcome, p.failure.code
	}
	return "", ""
}

// closing reports that the outcome is chosen and facts no longer change the route.
func (p *Process) closing() bool {
	return p.failure != nil || p.done || p.final != ""
}

func (p *Process) fail(outcome Outcome, code string) {
	if p.failure != nil {
		return
	}
	p.failure = &negative{outcome: outcome, code: code}
}
