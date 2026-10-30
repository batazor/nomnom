package order

import (
	"fmt"
	"time"
)

// FulfillmentFact is a business result of restaurants or delivery kept in the
// journal of an Order: when it happened and why. The position of the Order is its
// State alone and is never derived from the journal; waiting stages belong to
// the workflow.
type FulfillmentFact string

const (
	// FactPaymentAuthorized is recorded by the fulfillment workflow after the
	// approved Authorization of payments, before the restaurant is requested.
	FactPaymentAuthorized  FulfillmentFact = "payment_authorized"
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
	FactPaymentAuthorized,
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
	return o.recordFact(TriggerRecordFact, FactRestaurantRejected, occurredAt, reason)
}

// RecordPaymentAuthorization records the approved Authorization and moves the
// Order to paid. Unlike the facts of neighbours it never arrives late: the
// workflow records it before it requests the restaurant.
func (o *Order) RecordPaymentAuthorization(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(TriggerAuthorizePayment, FactPaymentAuthorized, occurredAt, "")
}

func (o *Order) RecordPreparationStarted(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(TriggerStartPreparation, FactPreparationStarted, occurredAt, "")
}

func (o *Order) RecordReadyForPickup(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(TriggerMarkReady, FactReadyForPickup, occurredAt, "")
}

func (o *Order) RecordCourierAssignment(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(TriggerRecordFact, FactCourierAssigned, occurredAt, "")
}

func (o *Order) RecordPickup(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(TriggerPickUp, FactPickedUp, occurredAt, "")
}

func (o *Order) RecordDeliveryCompletion(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(TriggerRecordFact, FactDeliveryCompleted, occurredAt, "")
}

func (o *Order) RecordDeliveryFailure(occurredAt time.Time, reason string) (TransitionResult, error) {
	return o.recordFact(TriggerRecordFact, FactDeliveryFailed, occurredAt, reason)
}

// recordFact lets the FSM decide the transition by state first, then enforces
// that each fact is in the journal at most once.
func (o *Order) recordFact(trigger Trigger, fact FulfillmentFact, occurredAt time.Time, reason string) (TransitionResult, error) {
	result, err := transition(o.state, trigger, TransitionInput{OccurredAt: occurredAt})
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
