package order

import (
	"fmt"
	"time"
)

// NegativeOutcome distinguishes an explicit Cancellation from a business Failure.
// Both currently end in StateCancelled; a separate failed state is not agreed yet.
type NegativeOutcome string

const (
	OutcomeCancellation NegativeOutcome = "cancellation"
	OutcomeFailure      NegativeOutcome = "failure"
)

// CancellationReason keeps the kind of negative outcome and a stable reason code.
type CancellationReason struct {
	outcome NegativeOutcome
	code    string
}

func NewCancellationReason(outcome NegativeOutcome, code string) (CancellationReason, error) {
	if (outcome != OutcomeCancellation && outcome != OutcomeFailure) || code == "" {
		return CancellationReason{}, ErrInvalidCancellationReason
	}
	return CancellationReason{outcome: outcome, code: code}, nil
}

func (r CancellationReason) Outcome() NegativeOutcome { return r.outcome }
func (r CancellationReason) Code() string             { return r.code }
func (r CancellationReason) IsZero() bool             { return r == CancellationReason{} }

// CancellationNotAllowed wraps the reasons of an unsatisfied WithinCancellationWindow policy.
type CancellationNotAllowed struct {
	Reasons []PolicyReason
}

func (e CancellationNotAllowed) Error() string {
	return fmt.Sprintf("order cancellation is not allowed: %v", e.Reasons)
}

// CancellationWindow is the cancellation policy interval supplied by the application.
type CancellationWindow struct {
	OpensAt  time.Time
	ClosesAt time.Time
}

// CheckCancellation reports whether a Cancellation Request is admissible at requestedAt
// without changing the Order: the window policy first, then the FSM row for cancel
// and the rule that an Order with a known outcome is settled by the workflow.
func (o *Order) CheckCancellation(requestedAt time.Time, window CancellationWindow) error {
	policy := WithinCancellationWindow(CancellationWindowContext{
		Now:      requestedAt,
		OpensAt:  window.OpensAt,
		ClosesAt: window.ClosesAt,
	})
	if !policy.Satisfied() {
		return CancellationNotAllowed(policy)
	}
	if _, err := transition(o.state, TriggerCancel, TransitionInput{RequestedAt: requestedAt}); err != nil {
		return err
	}
	if !o.state.AcceptsCancellation() {
		return InvalidTransition{From: o.state, Trigger: TriggerCancel}
	}
	return nil
}

// Cancel records the final negative outcome chosen by the fulfillment process.
func (o *Order) Cancel(requestedAt time.Time, reason CancellationReason) (TransitionResult, error) {
	if reason.IsZero() {
		return TransitionResult{}, ErrInvalidCancellationReason
	}
	result, err := o.apply(TriggerCancel, TransitionInput{RequestedAt: requestedAt})
	if err != nil {
		return TransitionResult{}, err
	}
	o.remember(TriggerCancel, requestedAt)
	o.cancellation = reason
	o.cancelledAt = requestedAt
	o.events = append(o.events, OrderCancelled{OrderID: o.id, MarketID: o.marketID, RequestedAt: requestedAt, Reason: reason})
	return result, nil
}
