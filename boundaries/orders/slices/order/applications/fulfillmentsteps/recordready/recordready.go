// Package recordready records that the Restaurant Order is ready for Pickup.
package recordready

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Consumer is the stable logical Inbox consumer name of this use case.
const Consumer = "orders.recordorderready"

// Input is built by the Kafka consumer adapter from the integration event.
type Input struct {
	EventID    string
	OrderID    order.ID
	OccurredAt time.Time
}

type Result struct {
	Outcome          fulfillment.Outcome
	WorkflowNotified bool
}

type Handler struct {
	uow      unitofwork.UnitOfWork
	inbox    unitofwork.Inbox
	orders   order.Repository
	workflow fulfillment.Notifier
}

func NewHandler(
	uow unitofwork.UnitOfWork,
	inbox unitofwork.Inbox,
	orders order.Repository,
	workflow fulfillment.Notifier,
) Handler {
	return Handler{uow: uow, inbox: inbox, orders: orders, workflow: workflow}
}

func (in Input) validate() error {
	switch {
	case in.EventID == "":
		return usecase.InvalidInput("event_id", "is required")
	case in.OrderID == "":
		return usecase.InvalidInput("order_id", "is required")
	case in.OccurredAt.IsZero():
		return usecase.InvalidInput("occurred_at", "is required")
	}
	return nil
}

// Handle runs unitofwork.AcceptOnce { load Order → record the step → save } and hands
// the signal to the workflow after commit, also for a duplicate: a crash between
// commit and the notification is repaired by redelivery. An error of the
// notification is returned, so the event is not acknowledged; a closed workflow
// is a no-op.
func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	if err := in.validate(); err != nil {
		return Result{}, err
	}
	var outcome fulfillment.Outcome
	accepted, err := unitofwork.AcceptOnce(ctx, h.uow, h.inbox, Consumer, in.EventID, func(ctx context.Context) error {
		var err error
		outcome, err = h.record(ctx, in)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	if !accepted {
		outcome = fulfillment.OutcomeDuplicate
	}

	err = h.workflow.Notify(ctx, fulfillment.Notification{
		EventID:    in.EventID,
		OrderID:    in.OrderID,
		Kind:       fulfillment.KindReadyForPickup,
		OccurredAt: in.OccurredAt,
	})
	switch {
	case err == nil:
		return Result{Outcome: outcome, WorkflowNotified: true}, nil
	case errors.Is(err, fulfillment.ErrWorkflowNotFound):
		return Result{Outcome: outcome}, nil
	default:
		return Result{Outcome: outcome}, fmt.Errorf("notify order fulfillment workflow: %w", err)
	}
}

// record applies the signal to the Order inside the Inbox unit of work; only
// an applied signal saves the Order.
func (h Handler) record(ctx context.Context, in Input) (fulfillment.Outcome, error) {
	current, err := h.orders.Get(ctx, in.OrderID)
	if err != nil {
		return "", err
	}
	_, err = current.RecordReadyForPickup(in.OccurredAt)
	outcome, err := fulfillment.ClassifySignal(current, err)
	if err != nil || outcome != fulfillment.OutcomeApplied {
		return outcome, err
	}
	return outcome, h.orders.Save(ctx, current)
}
