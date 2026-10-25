package order

import (
	"fmt"
	"time"
)

// FulfillmentFact is a business result of restaurants or delivery reflected in an
// Order. Facts are Order data, not Order States: waiting stages belong to the workflow.
type FulfillmentFact string

const (
	FactRestaurantAccepted FulfillmentFact = "restaurant_accepted"
	FactRestaurantRejected FulfillmentFact = "restaurant_rejected"
	FactPreparationStarted FulfillmentFact = "preparation_started"
	FactReadyForPickup     FulfillmentFact = "ready_for_pickup"
	FactCourierAssigned    FulfillmentFact = "courier_assigned"
	FactPickedUp           FulfillmentFact = "picked_up"
	FactDeliveryCompleted  FulfillmentFact = "delivery_completed"
	FactDeliveryFailed     FulfillmentFact = "delivery_failed"
)

var allFacts = [...]FulfillmentFact{
	FactRestaurantAccepted,
	FactRestaurantRejected,
	FactPreparationStarted,
	FactReadyForPickup,
	FactCourierAssigned,
	FactPickedUp,
	FactDeliveryCompleted,
	FactDeliveryFailed,
}

// RecordedFact is a fact with the time reported by its owner and an optional reason.
type RecordedFact struct {
	Fact       FulfillmentFact
	OccurredAt time.Time
	Reason     string
}

// FactAlreadyRecorded is returned when the same fact is reported again.
type FactAlreadyRecorded struct {
	Fact FulfillmentFact
}

func (e FactAlreadyRecorded) Error() string {
	return fmt.Sprintf("order fact %q is already recorded", e.Fact)
}

func isKnownFact(fact FulfillmentFact) bool {
	for _, candidate := range allFacts {
		if candidate == fact {
			return true
		}
	}
	return false
}

func (o *Order) RecordRestaurantRejection(occurredAt time.Time, reason string) (TransitionResult, error) {
	return o.recordFact(FactRestaurantRejected, occurredAt, reason)
}

func (o *Order) RecordPreparationStarted(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(FactPreparationStarted, occurredAt, "")
}

func (o *Order) RecordReadyForPickup(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(FactReadyForPickup, occurredAt, "")
}

func (o *Order) RecordCourierAssignment(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(FactCourierAssigned, occurredAt, "")
}

func (o *Order) RecordPickup(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(FactPickedUp, occurredAt, "")
}

func (o *Order) RecordDeliveryCompletion(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(FactDeliveryCompleted, occurredAt, "")
}

func (o *Order) RecordDeliveryFailure(occurredAt time.Time, reason string) (TransitionResult, error) {
	return o.recordFact(FactDeliveryFailed, occurredAt, reason)
}

// recordFact lets the FSM decide admissibility by state first, then enforces
// that each fact is recorded at most once. Facts are not ordered here: the
// sequence of Preparation and Delivery belongs to their owning contexts.
func (o *Order) recordFact(fact FulfillmentFact, occurredAt time.Time, reason string) (TransitionResult, error) {
	result, err := transition(o.state, TriggerRecordFact, TransitionInput{OccurredAt: occurredAt})
	if err != nil {
		return TransitionResult{}, err
	}
	if o.HasFact(fact) {
		return TransitionResult{}, FactAlreadyRecorded{Fact: fact}
	}
	o.facts = append(o.facts, RecordedFact{Fact: fact, OccurredAt: occurredAt, Reason: reason})
	o.state = result.To
	return result, nil
}

func (o *Order) HasFact(fact FulfillmentFact) bool {
	for _, recorded := range o.facts {
		if recorded.Fact == fact {
			return true
		}
	}
	return false
}

// Facts returns recorded facts in recording order.
func (o *Order) Facts() []RecordedFact { return append([]RecordedFact(nil), o.facts...) }
