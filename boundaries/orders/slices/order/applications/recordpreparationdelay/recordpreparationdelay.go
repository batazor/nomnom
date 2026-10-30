// Package recordpreparationdelay reflects PreparationDelayed of restaurants:
// the Order takes the new expected ready time (FR-611) and the fulfillment
// workflow extends its preparation deadline. A delay is not a Fulfillment
// step: every delay replaces the ready time named before.
package recordpreparationdelay

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
	case in.ExpectedReadyAt.IsZero():
		return usecase.InvalidInput("expected_ready_at", "is required")
	case in.EventID == "":
		return usecase.InvalidInput("event_id", "is required")
	case in.OrderID == "":
		return usecase.InvalidInput("order_id", "is required")
	case in.OccurredAt.IsZero():
		return usecase.InvalidInput("occurred_at", "is required")
	}
	return nil
}

// Handle runs unitofwork.AcceptOnce { load Order → replace the ready time → save } and hands
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
		outcome, err = h.replace(ctx, in)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	if !accepted {
		outcome = fulfillment.OutcomeDuplicate
	}

	err = h.workflow.Notify(ctx, fulfillment.Notification{
		EventID:         in.EventID,
		OrderID:         in.OrderID,
		Kind:            fulfillment.KindPreparationDelayed,
		OccurredAt:      in.OccurredAt,
		ExpectedReadyAt: in.ExpectedReadyAt,
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

// replace applies the signal to the Order inside the Inbox unit of work; only
// an applied signal saves the Order.
func (h Handler) replace(ctx context.Context, in Input) (fulfillment.Outcome, error) {
	current, err := h.orders.Get(ctx, in.OrderID)
	if err != nil {
		return "", err
	}
	_, err = current.ExpectReadyAt(in.OccurredAt, in.ExpectedReadyAt)
	outcome, err := fulfillment.ClassifySignal(current, err)
	if err != nil || outcome != fulfillment.OutcomeApplied {
		return outcome, err
	}
	return outcome, h.orders.Save(ctx, current)
}
