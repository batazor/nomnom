// Package relaypreparationissue hands PreparationFailed of restaurants to the
// fulfillment workflow. It is an orchestration input, not a step of the Order FSM
// of the Order, so the use case changes no local state and uses no Inbox: the
// workflow deduplicates it by event_id. PreparationDelayed changes the
// expected ready time of the Order and belongs to recordpreparationdelay.
package relaypreparationissue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Input is built by the Kafka consumer adapter from the integration event.
type Input struct {
	EventID    string
	OrderID    order.ID
	OccurredAt time.Time
	Reason     string
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

type Result struct {
	WorkflowNotified bool
}

type Handler struct {
	workflow fulfillment.Notifier
}

func NewHandler(workflow fulfillment.Notifier) Handler {
	return Handler{workflow: workflow}
}

func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	if err := in.validate(); err != nil {
		return Result{}, err
	}
	err := h.workflow.Notify(ctx, fulfillment.Notification{
		OrderID:    in.OrderID,
		EventID:    in.EventID,
		Kind:       fulfillment.KindPreparationFailed,
		OccurredAt: in.OccurredAt,
		Reason:     in.Reason,
	})
	switch {
	case err == nil:
		return Result{WorkflowNotified: true}, nil
	case errors.Is(err, fulfillment.ErrWorkflowNotFound):
		return Result{}, nil
	default:
		return Result{}, fmt.Errorf("notify order fulfillment workflow: %w", err)
	}
}
