package fulfillment

import "time"

// FactKind is a business fact of the Order or of a neighbouring context.
type FactKind string

const (
	// Confirmations: the process already knows the result synchronously.
	FactOrderPlaced       FactKind = "OrderPlaced"
	FactPaymentAuthorized FactKind = "PaymentAuthorized"
	FactPaymentFailed     FactKind = "PaymentFailed"

	FactRestaurantOrderAccepted   FactKind = "RestaurantOrderAccepted"
	FactRestaurantOrderRejected   FactKind = "RestaurantOrderRejected"
	FactPreparationStarted        FactKind = "PreparationStarted"
	FactPreparationDelayed        FactKind = "PreparationDelayed"
	FactPreparationFailed         FactKind = "PreparationFailed"
	FactRestaurantOrderReady      FactKind = "RestaurantOrderReady"
	FactCourierAssignedToDelivery FactKind = "CourierAssignedToDelivery"
	FactDeliveryPickedUp          FactKind = "DeliveryPickedUp"
	FactDeliveryCompleted         FactKind = "DeliveryCompleted"
	FactDeliveryFailed            FactKind = "DeliveryFailed"
)

// Fact is an incoming fact identified by the id of its Integration Event.
type Fact struct {
	Kind    FactKind
	EventID string
	// ExpectedReadyAt is set only for PreparationDelayed.
	ExpectedReadyAt time.Time
}

// Verdict says how the process treated an incoming fact.
type Verdict int

const (
	// Applied: the fact is admitted and may change the route.
	Applied Verdict = iota
	// MissingEventID: a fact without id cannot be deduplicated and is ignored.
	MissingEventID
	// Duplicate: the id was already processed.
	Duplicate
	// Closed: the outcome is chosen, facts no longer change the route.
	Closed
	// Refused: the current state does not admit the fact; Effect.Reason says why.
	Refused
)

// Effect is the result of applying a fact.
type Effect struct {
	Verdict Verdict
	// Reason is the diagnostic reason of Refused.
	Reason string
}

// Changed reports that the process admitted the input and must re-evaluate its route.
func (e Effect) Changed() bool { return e.Verdict == Applied }

// Apply applies a fact received at the given time. Ids are deduplicated.
func (p *Process) Apply(fact Fact, at time.Time) Effect {
	return p.receive(fact.EventID, func() string { return p.admit(fact, at) })
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

// admit changes the process for an admitted fact; a non-empty result is the
// diagnostic reason why the current state does not admit it.
func (p *Process) admit(fact Fact, at time.Time) string {
	switch fact.Kind {
	case FactRestaurantOrderAccepted:
		return p.onAccepted(at)
	case FactRestaurantOrderRejected:
		return p.onRejected()
	case FactPreparationStarted:
		return p.onPreparationStarted()
	case FactPreparationDelayed:
		return p.onPreparationDelayed(fact.ExpectedReadyAt)
	case FactPreparationFailed:
		return p.onPreparationFailed()
	case FactRestaurantOrderReady:
		return p.onReady(at)
	case FactCourierAssignedToDelivery:
		return p.onAssigned(at)
	case FactDeliveryPickedUp:
		return p.onPickedUp(at)
	case FactDeliveryCompleted:
		return p.onDelivered()
	case FactDeliveryFailed:
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
