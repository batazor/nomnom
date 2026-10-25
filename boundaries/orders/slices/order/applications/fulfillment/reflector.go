package fulfillment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
)

// Outcome classifies how an inbound business fact affected the Order.
type Outcome string

const (
	OutcomeApplied        Outcome = "applied"
	OutcomeAlreadyApplied Outcome = "already_applied"
	OutcomeIgnoredLate    Outcome = "ignored_late"
	OutcomeDuplicate      Outcome = "duplicate"
)

// InboundFact is the part of an integration event that a reflecting use case needs.
type InboundFact struct {
	Consumer   string
	EventID    string
	OrderID    order.ID
	Kind       Kind
	OccurredAt time.Time
	Reason     string
}

func (f InboundFact) validate() error {
	switch {
	case f.Consumer == "":
		return application.InvalidInput("consumer", "is required")
	case f.EventID == "":
		return application.InvalidInput("event_id", "is required")
	case f.OrderID == "":
		return application.InvalidInput("order_id", "is required")
	case f.OccurredAt.IsZero():
		return application.InvalidInput("occurred_at", "is required")
	}
	return nil
}

// Reflection is the observable result of reflecting one inbound fact.
type Reflection struct {
	Outcome          Outcome
	WorkflowNotified bool
}

// Reflector runs the insert-first Inbox protocol shared by the use cases that
// reflect results of restaurants and delivery:
//
//	UoW { Inbox.Accept → load Order → domain method → Save } → commit → Notify
//
// A duplicate event changes nothing and notifies the workflow again with the
// same event_id, so a crash between commit and notification is repaired by
// ordinary redelivery. A closed workflow is a diagnosable no-op.
type Reflector struct {
	uow      application.UnitOfWork
	inbox    application.Inbox
	orders   order.Repository
	notifier Notifier
}

func NewReflector(uow application.UnitOfWork, inbox application.Inbox, orders order.Repository, notifier Notifier) Reflector {
	return Reflector{uow: uow, inbox: inbox, orders: orders, notifier: notifier}
}

func (r Reflector) Reflect(ctx context.Context, fact InboundFact, apply func(*order.Order) error) (Reflection, error) {
	if err := fact.validate(); err != nil {
		return Reflection{}, err
	}

	var outcome Outcome
	err := r.uow.Do(ctx, func(ctx context.Context) error {
		accepted, err := r.inbox.Accept(ctx, fact.Consumer, fact.EventID)
		if err != nil {
			return fmt.Errorf("accept inbox event: %w", err)
		}
		if !accepted {
			outcome = OutcomeDuplicate
			return nil
		}

		current, err := r.orders.Get(ctx, fact.OrderID)
		if err != nil {
			return err
		}
		outcome, err = classify(current, apply(current))
		if err != nil {
			return err
		}
		if outcome != OutcomeApplied {
			return nil
		}
		return r.orders.Save(ctx, current)
	})
	if err != nil {
		return Reflection{}, err
	}

	reflection := Reflection{Outcome: outcome}
	err = r.notifier.Notify(ctx, Notification{
		OrderID:    fact.OrderID,
		EventID:    fact.EventID,
		Kind:       fact.Kind,
		OccurredAt: fact.OccurredAt,
		Reason:     fact.Reason,
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
// inbound fact: a repeated fact is a no-op, a fact for a final Order is late.
func classify(current *order.Order, err error) (Outcome, error) {
	var alreadyInState order.AlreadyInState
	var alreadyRecorded order.FactAlreadyRecorded
	var invalid order.InvalidTransition
	switch {
	case err == nil:
		return OutcomeApplied, nil
	case errors.As(err, &alreadyInState), errors.As(err, &alreadyRecorded):
		return OutcomeAlreadyApplied, nil
	case errors.As(err, &invalid) && current.State().IsFinal():
		return OutcomeIgnoredLate, nil
	default:
		return "", err
	}
}
