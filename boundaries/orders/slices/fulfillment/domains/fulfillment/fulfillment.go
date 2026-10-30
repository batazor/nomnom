// Package fulfillment is the domain model of the Order Fulfillment process:
// the journal of confirmed steps, signals of neighbouring contexts and deadlines. Operations return decisions instead of performing
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
	ReasonPickupTimeout               = "pickup_timeout"
	ReasonDeliveryFailed              = "delivery_failed"
	ReasonDeliveryTimeout             = "delivery_timeout"
	ReasonCustomerRequested           = "customer_requested"
	// ReasonSlotUnavailable: the hold of the Delivery Window expired and the
	// window filled up before ConfirmSlotReservation (FR-682).
	ReasonSlotUnavailable = "slot_unavailable"
)

// ReasonCodes lists every reason code the process can record on an Order.
func ReasonCodes() []string {
	return []string{
		ReasonPaymentDeclined, ReasonPaymentCaptureDeclined,
		ReasonRestaurantRejected, ReasonRestaurantAcceptanceTimeout,
		ReasonPreparationFailed, ReasonPreparationTimeout,
		ReasonPickupTimeout, ReasonDeliveryFailed, ReasonDeliveryTimeout,
		ReasonCustomerRequested, ReasonSlotUnavailable,
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
	paymentRecorded     bool
	captured            bool
	restaurantRequested bool
	restaurantClosed    bool
	deliveryCreated     bool
	deliveryClosed      bool

	// reportsPickup: the route tells the kitchen about the Pickup. A run recorded
	// before this step keeps its route.
	reportsPickup            bool
	restaurantPickupRecorded bool
	pickedUpAt               time.Time

	accepted bool
	ready    bool
	assigned bool
	pickedUp bool
	done     bool

	acceptanceDeadline  time.Time
	preparationDeadline time.Time
	preparationLimit    time.Time
	pickupDeadline      time.Time
	deliveryDeadline    time.Time

	failure *negative

	// schedule is the scheduled route of a Scheduled Order; nil on the ASAP
	// route.
	schedule *schedule
}

// New starts the process of an ASAP Order with the deadlines fixed for its
// whole life.
func New(policy Policy) *Process {
	return &Process{
		policy:        policy,
		processed:     map[string]bool{},
		cancellations: map[string]bool{},
	}
}

// NewScheduled starts the process of a Scheduled Order: its Delivery Window is
// held in restaurants since Place Order, so the Saga releases it (FR-682).
func NewScheduled(policy Policy) *Process {
	p := New(policy)
	p.schedule = &schedule{}
	return p
}

// ReportPickupToRestaurant adds the step that tells the kitchen about the Pickup.
func (p *Process) ReportPickupToRestaurant() { p.reportsPickup = true }

// PickedUpAt is the moment of the Pickup; zero before it.
func (p *Process) PickedUpAt() time.Time { return p.pickedUpAt }

// Failed reports that a negative outcome is chosen and the Saga must run.
func (p *Process) Failed() bool { return p.failure != nil && p.final == "" }

// Completed reports that the Order completed.
func (p *Process) Completed() bool { return p.final == StageCompleted }

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

// closing reports that the outcome is chosen and signals no longer change the route.
func (p *Process) closing() bool {
	return p.failure != nil || p.done || p.final != ""
}

func (p *Process) fail(outcome Outcome, code string) {
	if p.failure != nil {
		return
	}
	p.failure = &negative{outcome: outcome, code: code}
}
