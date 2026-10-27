// Package startfulfillment starts the Order Fulfillment Workflow from the own
// OrderPlaced of orders (ADR 5). It is the only start path: Place Order commits
// the Order and OrderPlaced to the Outbox and does not call Temporal.
package startfulfillment

import (
	"context"
	"fmt"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Consumer is the Inbox consumer name of the use case.
const Consumer = "orders.startfulfillment"

// Input is built by the Kafka consumer adapter from OrderPlaced.
type Input struct {
	EventID    string
	OrderID    order.ID
	OccurredAt time.Time
}

func (in Input) validate() error {
	switch {
	case in.EventID == "":
		return application.InvalidInput("event_id", "is required")
	case in.OrderID == "":
		return application.InvalidInput("order_id", "is required")
	case in.OccurredAt.IsZero():
		return application.InvalidInput("occurred_at", "is required")
	}
	return nil
}

// Result reports the Inbox outcome and whether the workflow was started or
// signalled.
type Result struct {
	Outcome          fulfillment.Outcome
	WorkflowNotified bool
}

type Handler struct {
	uow      application.UnitOfWork
	inbox    application.Inbox
	orders   order.Repository
	workflow fulfillment.Starter
}

func NewHandler(uow application.UnitOfWork, inbox application.Inbox, orders order.Repository, workflow fulfillment.Starter) Handler {
	return Handler{uow: uow, inbox: inbox, orders: orders, workflow: workflow}
}

// Handle records OrderPlaced in the Inbox together with reading the Order and
// starts the workflow after commit, also for a duplicate: a crash between
// commit and the start is repaired by redelivery. A final Order is not started
// again — its closed workflow may already be gone from the namespace.
func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	if err := in.validate(); err != nil {
		return Result{}, err
	}

	var final bool
	readOrder := func(ctx context.Context) error {
		current, err := h.orders.Get(ctx, in.OrderID)
		if err != nil {
			return err
		}
		final = current.State().IsFinal()
		return nil
	}
	accepted, err := application.AcceptOnce(ctx, h.uow, h.inbox, Consumer, in.EventID, readOrder)
	if err != nil {
		return Result{}, err
	}
	outcome := fulfillment.OutcomeApplied
	if !accepted {
		outcome = fulfillment.OutcomeDuplicate
		if err := h.uow.Do(ctx, readOrder); err != nil {
			return Result{}, err
		}
	}
	if final {
		return Result{Outcome: fulfillment.OutcomeIgnoredLate}, nil
	}

	err = h.workflow.Start(ctx, fulfillment.StartRequest{OrderID: in.OrderID, EventID: in.EventID, OccurredAt: in.OccurredAt})
	if err != nil {
		return Result{Outcome: outcome}, fmt.Errorf("%w: start order fulfillment workflow: %w", application.ErrDependencyUnavailable, err)
	}
	return Result{Outcome: outcome, WorkflowNotified: true}, nil
}
