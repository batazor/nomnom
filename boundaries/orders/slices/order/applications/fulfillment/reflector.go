package fulfillment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
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

// InboundSignal is the part of an integration event that a reflecting use case needs.
type InboundSignal struct {
	Consumer   string
	EventID    string
	OrderID    order.ID
	Kind       Kind
	OccurredAt time.Time
	Reason     string
	// ExpectedReadyAt is handed to the workflow for KindPreparationDelayed.
	ExpectedReadyAt time.Time
}

func (s InboundSignal) validate() error {
	switch {
	case s.Consumer == "":
		return usecase.InvalidInput("consumer", "is required")
	case s.EventID == "":
		return usecase.InvalidInput("event_id", "is required")
	case s.OrderID == "":
		return usecase.InvalidInput("order_id", "is required")
	case s.OccurredAt.IsZero():
		return usecase.InvalidInput("occurred_at", "is required")
	}
	return nil
}

// Reflection is the observable result of reflecting one inbound signal.
type Reflection struct {
	Outcome          Outcome
	WorkflowNotified bool
}

// Reflector runs the insert-first Inbox protocol (unitofwork.AcceptOnce)
// shared by the use cases that reflect results of restaurants and delivery:
//
//	UoW { Inbox.Accept → load Order → domain method → Save } → commit → Notify
//
// A duplicate event changes nothing and notifies the workflow again with the
// same event_id, so a crash between commit and notification is repaired by
// ordinary redelivery. A closed workflow is a diagnosable no-op.
type Reflector struct {
	uow      unitofwork.UnitOfWork
	inbox    unitofwork.Inbox
	orders   order.Repository
	notifier Notifier
}

func NewReflector(uow unitofwork.UnitOfWork, inbox unitofwork.Inbox, orders order.Repository, notifier Notifier) Reflector {
	return Reflector{uow: uow, inbox: inbox, orders: orders, notifier: notifier}
}

// Reflect applies a step of the Order FSM; an applied step saves the Order.
func (r Reflector) Reflect(ctx context.Context, signal InboundSignal, apply func(*order.Order) error) (Reflection, error) {
	return r.reflect(ctx, signal, func(_ context.Context, current *order.Order) (Outcome, error) {
		return classify(current, apply(current))
	})
}

// ChangeDetails runs inside the Inbox unit of work, before the Order is saved
// and committed, for data that is not a position of the Order and for side
// effects that must commit with the Inbox record: a relationship write of the
// dual-write rule in docs/security.md and the business data it derives from.
// Its error rolls the event back, so redelivery repeats it; it must therefore be
// idempotent. changed reports whether the Order is to be saved.
type ChangeDetails func(ctx context.Context, current *order.Order) (changed bool, err error)

// ReflectDetails reflects a signal that moves no State. admit lets the FSM refuse
// a final Order, which makes the signal late; a change is applied, no change is
// already applied.
func (r Reflector) ReflectDetails(ctx context.Context, signal InboundSignal, admit func(*order.Order) error, change ChangeDetails) (Reflection, error) {
	return r.reflect(ctx, signal, func(ctx context.Context, current *order.Order) (Outcome, error) {
		outcome, err := classify(current, admit(current))
		if err != nil || outcome != OutcomeApplied {
			return outcome, err
		}
		changed, err := change(ctx, current)
		switch {
		case err != nil:
			return "", err
		case !changed:
			return OutcomeAlreadyApplied, nil
		}
		return OutcomeApplied, nil
	})
}

func (r Reflector) reflect(
	ctx context.Context,
	signal InboundSignal,
	decide func(context.Context, *order.Order) (Outcome, error),
) (Reflection, error) {
	if err := signal.validate(); err != nil {
		return Reflection{}, err
	}

	var outcome Outcome
	accepted, err := unitofwork.AcceptOnce(ctx, r.uow, r.inbox, signal.Consumer, signal.EventID, func(ctx context.Context) error {
		current, err := r.orders.Get(ctx, signal.OrderID)
		if err != nil {
			return err
		}
		outcome, err = decide(ctx, current)
		if err != nil || outcome != OutcomeApplied {
			return err
		}
		return r.orders.Save(ctx, current)
	})
	if err != nil {
		return Reflection{}, err
	}
	if !accepted {
		outcome = OutcomeDuplicate
	}

	reflection := Reflection{Outcome: outcome}
	err = r.notifier.Notify(ctx, Notification{
		OrderID:         signal.OrderID,
		EventID:         signal.EventID,
		Kind:            signal.Kind,
		OccurredAt:      signal.OccurredAt,
		Reason:          signal.Reason,
		ExpectedReadyAt: signal.ExpectedReadyAt,
	})
	switch {
	case err == nil:
		reflection.WorkflowNotified = true
	case errors.Is(err, ErrWorkflowNotFound):
	default:
		return reflection, fmt.Errorf("notify order fulfillment workflow: %w", err)
	}
	return reflection, nil
}

// classify interprets strict FSM and aggregate errors by the semantics of an
// inbound signal: a repeated step is a no-op, a signal for a final Order is late.
func classify(current *order.Order, err error) (Outcome, error) {
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
