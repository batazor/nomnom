// Package recordpreparationdelay reflects PreparationDelayed of restaurants:
// the Order takes the new expected ready time (FR-611) and the fulfillment
// workflow extends its preparation deadline. A delay is not a Fulfillment
// step: every delay replaces the ready time named before.
package recordpreparationdelay

import (
	"context"
	"time"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Consumer is the stable logical Inbox consumer name of this use case.
const Consumer = "orders.recordpreparationdelay"

// Input is built by the Kafka consumer adapter from the integration event.
type Input struct {
	EventID    string
	OrderID    order.ID
	OccurredAt time.Time
	// ExpectedReadyAt is the new ready time named by the restaurant; required.
	ExpectedReadyAt time.Time
}

type Result struct {
	Outcome          fulfillment.Outcome
	WorkflowNotified bool
}

type Handler struct {
	reflector fulfillment.Reflector
}

func NewHandler(
	uow unitofwork.UnitOfWork,
	inbox unitofwork.Inbox,
	orders order.Repository,
	workflow fulfillment.Notifier,
) Handler {
	return Handler{reflector: fulfillment.NewReflector(uow, inbox, orders, workflow)}
}

func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	if in.ExpectedReadyAt.IsZero() {
		return Result{}, usecase.InvalidInput("expected_ready_at", "is required")
	}
	signal := fulfillment.InboundSignal{
		Consumer:        Consumer,
		EventID:         in.EventID,
		OrderID:         in.OrderID,
		Kind:            fulfillment.KindPreparationDelayed,
		OccurredAt:      in.OccurredAt,
		ExpectedReadyAt: in.ExpectedReadyAt,
	}
	reflection, err := h.reflector.Reflect(ctx, signal, func(current *order.Order) error {
		_, err := current.ExpectReadyAt(in.OccurredAt, in.ExpectedReadyAt)
		return err
	})
	return Result(reflection), err
}
