// Package completeorder records the successful outcome chosen by the fulfillment
// workflow. A repeated Activity after commit is a no-op without a second event.
package completeorder

import (
	"context"
	"errors"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
)

// Input carries the completion moment chosen by the fulfillment workflow. It
// is the same on every Activity attempt, so OrderCompleted keeps one time
// whichever attempt commits.
type Input struct {
	OrderID     order.ID
	CompletedAt time.Time
}

type Outcome string

const (
	OutcomeCompleted        Outcome = "completed"
	OutcomeAlreadyCompleted Outcome = "already_completed"
)

type Result struct {
	Outcome Outcome
}

// Outbox stores published domain events in the active transaction.
type Outbox interface {
	Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error
}

type Handler struct {
	uow    application.UnitOfWork
	orders order.Repository
	outbox Outbox
}

func NewHandler(uow application.UnitOfWork, orders order.Repository, outbox Outbox) Handler {
	return Handler{uow: uow, orders: orders, outbox: outbox}
}

func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	switch {
	case in.OrderID == "":
		return Result{}, application.InvalidInput("order_id", "is required")
	case in.CompletedAt.IsZero():
		return Result{}, application.InvalidInput("completed_at", "is required")
	}

	var result Result
	err := h.uow.Do(ctx, func(ctx context.Context) error {
		current, err := h.orders.Get(ctx, in.OrderID)
		if err != nil {
			return err
		}
		var already order.AlreadyInState
		switch _, err := current.Complete(in.CompletedAt); {
		case errors.As(err, &already):
			result = Result{Outcome: OutcomeAlreadyCompleted}
			return nil
		case err != nil:
			return err
		}
		if err := h.orders.Save(ctx, current); err != nil {
			return err
		}
		result = Result{Outcome: OutcomeCompleted}
		return h.outbox.Append(ctx, current.ID(), current.Version()+1, current.PullEvents())
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
