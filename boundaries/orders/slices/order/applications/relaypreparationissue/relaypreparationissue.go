// Package relaypreparationissue hands PreparationDelayed and PreparationFailed of
// restaurants to the fulfillment workflow. They are orchestration inputs, not
// Fulfillment Facts of the Order, so the use case changes no local state and
// uses no Inbox: the workflow deduplicates them by event_id.
package relaypreparationissue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Input is built by the Kafka consumer adapter from the integration event.
type Input struct {
	EventID    string
	OrderID    order.ID
	Kind       fulfillment.Kind
	OccurredAt time.Time
	// Reason is set for KindPreparationFailed.
	Reason string
	// ExpectedReadyAt is required for KindPreparationDelayed.
	ExpectedReadyAt time.Time
}

func (in Input) validate() error {
	switch {
	case in.EventID == "":
		return application.InvalidInput("event_id", "is required")
	case in.OrderID == "":
		return application.InvalidInput("order_id", "is required")
	case in.OccurredAt.IsZero():
		return application.InvalidInput("occurred_at", "is required")
	case in.Kind != fulfillment.KindPreparationDelayed && in.Kind != fulfillment.KindPreparationFailed:
		return application.InvalidInput("kind", "must be preparation_delayed or preparation_failed")
	case in.Kind == fulfillment.KindPreparationDelayed && in.ExpectedReadyAt.IsZero():
		return application.InvalidInput("expected_ready_at", "is required")
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
		OrderID:         in.OrderID,
		EventID:         in.EventID,
		Kind:            in.Kind,
		OccurredAt:      in.OccurredAt,
		Reason:          in.Reason,
		ExpectedReadyAt: in.ExpectedReadyAt,
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
