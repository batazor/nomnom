package fulfillment

import (
	"errors"

	"nomnom/orders/slices/order/domains/order"
)

// Outcome classifies how an inbound signal affected the Order.
type Outcome string

const (
	OutcomeApplied        Outcome = "applied"
	OutcomeAlreadyApplied Outcome = "already_applied"
	OutcomeIgnoredLate    Outcome = "ignored_late"
	OutcomeDuplicate      Outcome = "duplicate"
)

// ClassifySignal interprets the result of a domain method by the semantics of
// an inbound signal: a repeated step is a no-op, a signal for a final Order is
// late, and any other error rolls the event back.
func ClassifySignal(current *order.Order, err error) (Outcome, error) {
	var alreadyInState order.AlreadyInState
	var alreadyApplied order.TransitionAlreadyApplied
	var invalid order.InvalidTransition
	switch {
	case err == nil:
		return OutcomeApplied, nil
	case errors.As(err, &alreadyInState), errors.As(err, &alreadyApplied):
		return OutcomeAlreadyApplied, nil
	case errors.As(err, &invalid) && current.State().IsFinal():
		return OutcomeIgnoredLate, nil
	default:
		return "", err
	}
}
