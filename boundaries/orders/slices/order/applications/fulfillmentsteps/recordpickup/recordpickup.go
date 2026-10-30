// Package recordpickup records Pickup of an Order by the assigned Courier.
package recordpickup

import (
	"context"
	"time"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Consumer is the stable logical Inbox consumer name of this use case.
const Consumer = "orders.recordpickup"

// Input is built by the Kafka consumer adapter from the integration event.
type Input struct {
	EventID    string
	OrderID    order.ID
	OccurredAt time.Time
	// ExpectedDeliveredAt is the handover time named by the courier; zero — not named.
	ExpectedDeliveredAt time.Time
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
	signal := fulfillment.InboundSignal{
		Consumer:   Consumer,
		EventID:    in.EventID,
		OrderID:    in.OrderID,
		Kind:       fulfillment.KindPickedUp,
		OccurredAt: in.OccurredAt,
	}
	reflection, err := h.reflector.Reflect(ctx, signal, func(current *order.Order) error {
		if _, err := current.RecordPickup(in.OccurredAt); err != nil {
			return err
		}
		_, err := current.ExpectDeliveredAt(in.OccurredAt, in.ExpectedDeliveredAt)
		return err
	})
	return Result(reflection), err
}
