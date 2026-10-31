// Package recordcourierassignment records Courier Assignment for the Delivery
// of an Order. A change of the linked Delivery records RelationshipsChanged in
// the same Inbox unit of work; orders-consumer then syncs order#delivery
// (ADR 18). The courier sees the Order through delivery->act_as_courier, which
// delivery owns; orders keeps the assignment as business data.
package recordcourierassignment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/syncrelationships"
	"nomnom/orders/slices/order/domains/order"
)

// Consumer is the stable logical Inbox consumer name of this use case.
const Consumer = "orders.recordcourierassignment"

// Input is built by the Kafka consumer adapter from the integration event.
type Input struct {
	EventID    string
	OrderID    order.ID
	OccurredAt time.Time
	// DeliveryVersion is the aggregate_version of the Delivery in the event
	// envelope; it orders the courier events of the Order.
	DeliveryVersion uint64
	// DeliveryID and CourierID name the assignment; an event without them
	// reaches the workflow and changes no relationships.
	DeliveryID string
	CourierID  string
	// ExpectedPickupAt is the pickup time named by the courier; zero — not named.
	ExpectedPickupAt time.Time
}

type Result struct {
	Outcome          fulfillment.Outcome
	WorkflowNotified bool
}

type Handler struct {
	uow         unitofwork.UnitOfWork
	inbox       unitofwork.Inbox
	orders      order.Repository
	workflow    fulfillment.Notifier
	assignments order.CourierAssignments
	changes     syncrelationships.Changes
}

func NewHandler(
	uow unitofwork.UnitOfWork,
	inbox unitofwork.Inbox,
	orders order.Repository,
	workflow fulfillment.Notifier,
	assignments order.CourierAssignments,
	changes syncrelationships.Changes,
) Handler {
	return Handler{uow: uow, inbox: inbox, orders: orders, workflow: workflow, assignments: assignments, changes: changes}
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

// Handle runs unitofwork.AcceptOnce { load Order → store the assignment → save }
// and hands the signal to the workflow after commit, also for a duplicate: a
// crash between commit and the notification is repaired by redelivery. An error
// of the notification is returned, so the event is not acknowledged; a closed
// workflow is a no-op.
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
		OrderID:    in.OrderID,
		EventID:    in.EventID,
		Kind:       fulfillment.KindCourierAssigned,
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

// record applies the signal inside the Inbox unit of work. The assignment moves
// no State, so the FSM only admits it: a final Order makes the signal late. A
// later event of the same Order is a reassignment and replaces the assignment,
// its expected pickup time and, for another Delivery, order#delivery; the Order
// is saved, so order_details and the Order Change Notification follow the
// assignment. An out-of-order event, or an event without names that moves no
// expected time, changes nothing and is already applied.
func (h Handler) record(ctx context.Context, in Input) (fulfillment.Outcome, error) {
	current, err := h.orders.Get(ctx, in.OrderID)
	if err != nil {
		return "", err
	}
	outcome, err := fulfillment.ClassifySignal(current, current.AdmitCourierAssignment(in.OccurredAt))
	if err != nil || outcome != fulfillment.OutcomeApplied {
		return outcome, err
	}

	superseded, err := h.assign(ctx, in)
	if err != nil {
		return "", err
	}
	if !superseded {
		return fulfillment.OutcomeAlreadyApplied, nil
	}
	moved, err := current.ExpectPickupAt(in.OccurredAt, in.ExpectedPickupAt)
	if err != nil {
		return "", err
	}
	named := in.DeliveryID != "" && in.CourierID != ""
	if !moved && !named {
		return fulfillment.OutcomeAlreadyApplied, nil
	}
	return fulfillment.OutcomeApplied, h.orders.Save(ctx, current)
}

// assign runs in the Inbox unit of work. Whether the event supersedes the
// stored assignment and whether it links another Delivery is decided by
// order.CourierAssignment; the use case stores the result and, for another
// Delivery, records RelationshipsChanged in the same transaction. A
// reassignment within the linked Delivery leaves the relationships as they
// are. superseded is false for an out-of-order event, which changes nothing;
// an event without names has nothing to order by and is the latest one.
func (h Handler) assign(ctx context.Context, in Input) (superseded bool, err error) {
	if in.DeliveryID == "" || in.CourierID == "" {
		return true, nil
	}
	next := order.CourierAssignment{DeliveryID: in.DeliveryID, CourierID: in.CourierID, AssignedAt: in.OccurredAt, DeliveryVersion: in.DeliveryVersion}
	stored, found, err := h.assignments.CourierAssignment(ctx, in.OrderID)
	if err != nil {
		return false, fmt.Errorf("read courier assignment: %w", err)
	}
	if found && !next.Supersedes(stored) {
		return false, nil
	}
	if err := h.assignments.RememberCourierAssignment(ctx, in.OrderID, next); err != nil {
		return false, fmt.Errorf("remember courier assignment: %w", err)
	}
	if !found || !next.LinksDelivery(stored) {
		if err := h.changes.RelationshipsChanged(ctx, in.OrderID); err != nil {
			return false, fmt.Errorf("record relationships change: %w", err)
		}
	}
	return true, nil
}
