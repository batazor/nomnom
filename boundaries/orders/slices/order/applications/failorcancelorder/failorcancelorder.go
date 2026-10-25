// Package failorcancelorder records the final negative outcome chosen by the
// fulfillment workflow after compensations. Cancellation and Failure both end in
// the cancelled Order State; the kind of outcome and its reason are preserved.
package failorcancelorder

import (
	"context"
	"errors"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
)

type Input struct {
	OrderID    order.ID
	Outcome    order.NegativeOutcome
	ReasonCode string
	// DecidedAt is the workflow time of the decision, passed by the Activity.
	DecidedAt time.Time
}

type Outcome string

const (
	OutcomeCancelled        Outcome = "cancelled"
	OutcomeAlreadyCancelled Outcome = "already_cancelled"
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
	if in.DecidedAt.IsZero() {
		return Result{}, application.InvalidInput("decided_at", "is required")
	}
	reason, err := order.NewCancellationReason(in.Outcome, in.ReasonCode)
	if err != nil {
		return Result{}, err
	}

	var result Result
	err = h.uow.Do(ctx, func(ctx context.Context) error {
		current, err := h.orders.Get(ctx, in.OrderID)
		if err != nil {
			return err
		}
		var already order.AlreadyInState
		switch _, err := current.Cancel(in.DecidedAt, reason); {
		case errors.As(err, &already):
			result = Result{Outcome: OutcomeAlreadyCancelled}
			return nil
		case err != nil:
			return err
		}
		if err := h.orders.Save(ctx, current); err != nil {
			return err
		}
		result = Result{Outcome: OutcomeCancelled}
		return h.outbox.Append(ctx, current.ID(), current.Version()+1, current.PullEvents())
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
