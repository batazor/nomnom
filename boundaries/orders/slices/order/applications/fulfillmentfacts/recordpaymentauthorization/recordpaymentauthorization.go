// Package recordpaymentauthorization records the approved Authorization chosen
// by the fulfillment workflow in the Order, before the restaurant is requested.
// A repeated Activity after commit is a no-op without a second fact.
package recordpaymentauthorization

import (
	"context"
	"errors"
	"time"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/domains/order"
)

// Input carries the decision moment chosen by the fulfillment workflow. It is
// the same on every Activity attempt, so the fact keeps one time whichever
// attempt commits.
type Input struct {
	OrderID      order.ID
	AuthorizedAt time.Time
}

type Outcome string

const (
	OutcomeRecorded        Outcome = "recorded"
	OutcomeAlreadyRecorded Outcome = "already_recorded"
)

type Result struct {
	Outcome Outcome
}

type Handler struct {
	uow    unitofwork.UnitOfWork
	orders order.Repository
}

func NewHandler(uow unitofwork.UnitOfWork, orders order.Repository) Handler {
	return Handler{uow: uow, orders: orders}
}

// Handle saves the fact; the store writes order_details and the Order Change
// Notification in the same transaction. No Integration Event is published:
// neighbouring contexts do not need the fact.
func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	switch {
	case in.OrderID == "":
		return Result{}, usecase.InvalidInput("order_id", "is required")
	case in.AuthorizedAt.IsZero():
		return Result{}, usecase.InvalidInput("authorized_at", "is required")
	}

	var result Result
	err := h.uow.Do(ctx, func(ctx context.Context) error {
		current, err := h.orders.Get(ctx, in.OrderID)
		if err != nil {
			return err
		}
		var already order.FactAlreadyRecorded
		switch _, err := current.RecordPaymentAuthorization(in.AuthorizedAt); {
		case errors.As(err, &already):
			result = Result{Outcome: OutcomeAlreadyRecorded}
			return nil
		case err != nil:
			return err
		}
		result = Result{Outcome: OutcomeRecorded}
		return h.orders.Save(ctx, current)
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
