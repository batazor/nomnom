// Package confirmorder reflects restaurant Order Acceptance as Order Confirmation.
package confirmorder

import (
	"context"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Consumer is the stable logical Inbox consumer name of this use case.
const Consumer = "orders.confirmorder"

// Input is built by the Kafka consumer adapter from the integration event.
type Input struct {
	EventID    string
	OrderID    order.ID
	OccurredAt time.Time
	// ExpectedReadyAt is the ready time named by the restaurant; zero — not named.
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
	uow application.UnitOfWork,
	inbox application.Inbox,
	orders order.Repository,
	workflow fulfillment.Notifier,
) Handler {
	return Handler{reflector: fulfillment.NewReflector(uow, inbox, orders, workflow)}
}

func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	fact := fulfillment.InboundFact{
		Consumer:   Consumer,
		EventID:    in.EventID,
		OrderID:    in.OrderID,
		Kind:       fulfillment.KindRestaurantAccepted,
		OccurredAt: in.OccurredAt,
	}
	reflection, err := h.reflector.Reflect(ctx, fact, func(current *order.Order) error {
		if _, err := current.Confirm(in.OccurredAt); err != nil {
			return err
		}
		_, err := current.ExpectReadyAt(in.OccurredAt, in.ExpectedReadyAt)
		return err
	})
	return Result(reflection), err
}
