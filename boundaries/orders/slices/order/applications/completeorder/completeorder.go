// Package completeorder records the successful outcome chosen by the fulfillment
// workflow. A repeated Activity after commit is a no-op without a second event.
package completeorder

import (
	"context"
	"errors"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
)

type Input struct {
	OrderID order.ID
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
	if in.OrderID == "" {
		return Result{}, application.InvalidInput("order_id", "is required")
	}

	var result Result
	err := h.uow.Do(ctx, func(ctx context.Context) error {
		current, err := h.orders.Get(ctx, in.OrderID)
		if err != nil {
			return err
		}
		var already order.AlreadyInState
		switch _, err := current.Complete(); {
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
