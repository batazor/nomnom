package order

import (
	"fmt"
	"time"
)

// AppliedTransition is one entry of the transition history of an Order: which
// trigger the FSM applied and when it happened according to its owner. The
// position of the Order is its State alone and is never derived from the history;
// statuses of neighbours belong to their contexts, waiting stages to the workflow.
type AppliedTransition struct {
	Trigger    Trigger
	OccurredAt time.Time
}

// TransitionAlreadyApplied is returned when a trigger of the history is reported again.
type TransitionAlreadyApplied struct {
	Trigger Trigger
}

func (e TransitionAlreadyApplied) Error() string {
	return fmt.Sprintf("order transition %q is already applied", e.Trigger)
}

// RecordRestaurantRejection moves the Order to rejected; the reason stays with
// restaurants and goes to the workflow, which chooses the cancellation code.
func (o *Order) RecordRestaurantRejection(occurredAt time.Time) (TransitionResult, error) {
	return o.applyStep(TriggerReject, occurredAt)
}

// RecordPaymentAuthorization moves the Order to paid after the approved
// Authorization; the status of the payment stays in payments. Unlike the steps
// of neighbours it never arrives late: the workflow applies it before it
// requests the restaurant.
func (o *Order) RecordPaymentAuthorization(occurredAt time.Time) (TransitionResult, error) {
	return o.applyStep(TriggerAuthorizePayment, occurredAt)
}

func (o *Order) RecordPreparationStarted(occurredAt time.Time) (TransitionResult, error) {
	return o.applyStep(TriggerStartPreparation, occurredAt)
}

func (o *Order) RecordReadyForPickup(occurredAt time.Time) (TransitionResult, error) {
	return o.applyStep(TriggerMarkReady, occurredAt)
}

func (o *Order) RecordPickup(occurredAt time.Time) (TransitionResult, error) {
	return o.applyStep(TriggerPickUp, occurredAt)
}

func (o *Order) RecordDeliveryCompletion(occurredAt time.Time) (TransitionResult, error) {
	return o.applyStep(TriggerDeliver, occurredAt)
}

// RecordDeliveryFailure moves the Order to delivery_failed; the reason stays
// with delivery and goes to the workflow, which chooses the cancellation code.
func (o *Order) RecordDeliveryFailure(occurredAt time.Time) (TransitionResult, error) {
	return o.applyStep(TriggerFailDelivery, occurredAt)
}

// AdmitCourierAssignment lets the FSM refuse a Courier Assignment of a final
// Order. The assignment itself is not a position of the Order: it is stored by
// CourierAssignments, and its states belong to Delivery.
func (o *Order) AdmitCourierAssignment(occurredAt time.Time) error {
	_, err := transition(o.state, TriggerUpdateDetails, TransitionInput{OccurredAt: occurredAt})
	return err
}

// applyStep lets the FSM decide the transition by state first, then enforces
// that each step is in the history at most once.
func (o *Order) applyStep(trigger Trigger, occurredAt time.Time) (TransitionResult, error) {
	result, err := transition(o.state, trigger, TransitionInput{OccurredAt: occurredAt})
	if err != nil {
		return TransitionResult{}, err
	}
	if o.HasApplied(trigger) {
		return TransitionResult{}, TransitionAlreadyApplied{Trigger: trigger}
	}
	o.remember(trigger, occurredAt)
	o.state = result.To
	return result, nil
}

func (o *Order) remember(trigger Trigger, occurredAt time.Time) {
	o.transitions = append(o.transitions, AppliedTransition{Trigger: trigger, OccurredAt: occurredAt})
}

// HasApplied reports whether the trigger is in the history of the Order.
func (o *Order) HasApplied(trigger Trigger) bool {
	for _, applied := range o.transitions {
		if applied.Trigger == trigger {
			return true
		}
	}
	return false
}

// Transitions returns the history in the order of application.
func (o *Order) Transitions() []AppliedTransition {
	return append([]AppliedTransition(nil), o.transitions...)
}
