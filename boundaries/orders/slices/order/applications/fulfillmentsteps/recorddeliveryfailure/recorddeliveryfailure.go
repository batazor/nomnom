// Package recorddeliveryfailure records a business failure of Delivery with its reason;
// the negative outcome is fixed later by failorcancelorder.
package recorddeliveryfailure

import (
	"context"
	"time"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Consumer is the stable logical Inbox consumer name of this use case.
const Consumer = "orders.recorddeliveryfailure"

// Input is built by the Kafka consumer adapter from the integration event.
type Input struct {
	EventID    string
	OrderID    order.ID
	OccurredAt time.Time
	// Reason is the optional reason reported by the owning context.
	Reason string
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
		Kind:       fulfillment.KindDeliveryFailed,
		OccurredAt: in.OccurredAt,
		Reason:     in.Reason,
	}
	reflection, err := h.reflector.Reflect(ctx, signal, func(current *order.Order) error {
		_, err := current.RecordDeliveryFailure(in.OccurredAt)
		return err
	})
	return Result(reflection), err
}
