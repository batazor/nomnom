package fulfillment

import "time"

// SignalKind is a business signal of the Order or of a neighbouring context.
type SignalKind string

const (
	// Confirmation: the process already knows the Order was placed.
	SignalOrderPlaced SignalKind = "OrderPlaced"

	SignalRestaurantOrderAccepted   SignalKind = "RestaurantOrderAccepted"
	SignalRestaurantOrderRejected   SignalKind = "RestaurantOrderRejected"
	SignalPreparationStarted        SignalKind = "PreparationStarted"
	SignalPreparationDelayed        SignalKind = "PreparationDelayed"
	SignalPreparationFailed         SignalKind = "PreparationFailed"
	SignalRestaurantOrderReady      SignalKind = "RestaurantOrderReady"
	SignalCourierAssignedToDelivery SignalKind = "CourierAssignedToDelivery"
	SignalDeliveryPickedUp          SignalKind = "DeliveryPickedUp"
	SignalDeliveryCompleted         SignalKind = "DeliveryCompleted"
	SignalDeliveryFailed            SignalKind = "DeliveryFailed"
)

// Signal is an incoming signal identified by the id of its Integration Event.
type Signal struct {
	Kind    SignalKind
	EventID string
	// ExpectedReadyAt is set only for PreparationDelayed.
	ExpectedReadyAt time.Time
}

// Verdict says how the process treated an incoming signal.
type Verdict int

const (
	// Applied: the signal is admitted and may change the route.
	Applied Verdict = iota
	// MissingEventID: a signal without id cannot be deduplicated and is ignored.
	MissingEventID
	// Duplicate: the id was already processed.
	Duplicate
	// Closed: the outcome is chosen, signals no longer change the route.
	Closed
	// Refused: the current state does not admit the signal; Effect.Reason says why.
	Refused
)

// Effect is the result of applying a signal.
type Effect struct {
	Verdict Verdict
	// Reason is the diagnostic reason of Refused.
	Reason string
}

// Changed reports that the process admitted the input and must re-evaluate its route.
func (e Effect) Changed() bool { return e.Verdict == Applied }

// Apply applies a signal received at the given time. Ids are deduplicated.
func (p *Process) Apply(signal Signal, at time.Time) Effect {
	return p.receive(signal.EventID, func() string { return p.admit(signal, at) })
}

// receive deduplicates by id, ignores input once the outcome is chosen and
// reports every input the current state does not admit.
func (p *Process) receive(id string, apply func() string) Effect {
	if id == "" {
		return Effect{Verdict: MissingEventID}
	}
	if p.processed[id] {
		return Effect{Verdict: Duplicate}
	}
	p.processed[id] = true
	if p.closing() {
		return Effect{Verdict: Closed}
	}
	if reason := apply(); reason != "" {
		return Effect{Verdict: Refused, Reason: reason}
	}
	return Effect{Verdict: Applied}
}

// admit changes the process for an admitted signal; a non-empty result is the
// diagnostic reason why the current state does not admit it.
func (p *Process) admit(signal Signal, at time.Time) string {
	switch signal.Kind {
	case SignalRestaurantOrderAccepted:
		return p.onAccepted(at)
	case SignalRestaurantOrderRejected:
		return p.onRejected()
	case SignalPreparationStarted:
		return p.onPreparationStarted()
	case SignalPreparationDelayed:
		return p.onPreparationDelayed(signal.ExpectedReadyAt)
	case SignalPreparationFailed:
		return p.onPreparationFailed()
	case SignalRestaurantOrderReady:
		return p.onReady(at)
	case SignalCourierAssignedToDelivery:
		return p.onAssigned(at)
	case SignalDeliveryPickedUp:
		return p.onPickedUp(at)
	case SignalDeliveryCompleted:
		return p.onDelivered()
	case SignalDeliveryFailed:
		return p.onDeliveryFailed()
	default:
		// Confirmations of OrderPlaced and the payment result.
		return ""
	}
}

func (p *Process) onAccepted(at time.Time) string {
	switch {
	case !p.restaurantRequested:
		return "restaurant order not requested"
	case p.accepted:
		return "already accepted"
	}
	p.accepted = true
	if p.schedule != nil {
		// The kitchen accepted a Scheduled Order hours ahead: Preparation
		// counts from QueueRestaurantOrder (FR-416).
		return ""
	}
	p.armPreparation(at)
	return ""
}

func (p *Process) armPreparation(at time.Time) {
	p.preparationDeadline = at.Add(p.policy.PreparationTimeout)
	p.preparationLimit = p.preparationDeadline.Add(p.policy.MaxPreparationExtension)
}

func (p *Process) onRejected() string {
	if p.pickedUp {
		return "rejected after pickup"
	}
	p.restaurantClosed = true
	p.fail(OutcomeFailure, ReasonRestaurantRejected)
	return ""
}

func (p *Process) onPreparationStarted() string {
	if !p.accepted {
		return "not accepted"
	}
	return ""
}

func (p *Process) onPreparationDelayed(expectedReadyAt time.Time) string {
	switch {
	case !p.accepted || p.ready:
		return "not preparing"
	case p.schedule != nil && !p.schedule.queued:
		return "not queued"
	case !expectedReadyAt.After(p.preparationDeadline):
		return "no extension"
	}
	p.preparationDeadline = expectedReadyAt
	if p.preparationDeadline.After(p.preparationLimit) {
		p.preparationDeadline = p.preparationLimit
	}
	return ""
}

func (p *Process) onPreparationFailed() string {
	if p.ready {
		return "already ready"
	}
	p.restaurantClosed = true
	p.fail(OutcomeFailure, ReasonPreparationFailed)
	return ""
}

func (p *Process) onReady(at time.Time) string {
	switch {
	case !p.accepted:
		return "not accepted"
	case p.ready:
		return "already ready"
	}
	p.ready = true
	p.armPickup(at)
	return ""
}

func (p *Process) onAssigned(at time.Time) string {
	switch {
	case !p.deliveryCreated:
		return "delivery not created"
	case p.assigned:
		return "already assigned"
	}
	p.assigned = true
	p.armPickup(at)
	return ""
}

func (p *Process) armPickup(at time.Time) {
	if p.ready && p.assigned {
		p.pickupDeadline = at.Add(p.policy.PickupTimeout)
	}
}

func (p *Process) onPickedUp(at time.Time) string {
	switch {
	case !p.assigned:
		return "courier not assigned"
	case p.pickedUp:
		return "already picked up"
	}
	p.pickedUp = true
	p.pickedUpAt = at
	p.deliveryDeadline = at.Add(p.policy.DeliveryTimeout)
	return ""
}

func (p *Process) onDelivered() string {
	if !p.pickedUp {
		return "not picked up"
	}
	p.done = true
	return ""
}

func (p *Process) onDeliveryFailed() string {
	if !p.deliveryCreated {
		return "delivery not created"
	}
	p.deliveryClosed = true
	p.fail(OutcomeFailure, ReasonDeliveryFailed)
	return ""
}
