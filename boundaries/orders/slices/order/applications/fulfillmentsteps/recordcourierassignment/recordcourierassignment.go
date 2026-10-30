// Package recordcourierassignment records Courier Assignment for the Delivery
// of an Order. A change of the linked Delivery records RelationshipsChanged in
// the same Inbox unit of work; orders-consumer then syncs order#delivery
// (ADR 18). The courier sees the Order through delivery->act_as_courier, which
// delivery owns; orders keeps the assignment as business data.
package recordcourierassignment

import (
	"context"
	"fmt"
	"time"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
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
	reflector   fulfillment.Reflector
	assignments order.CourierAssignments
	changes     syncorderrelationships.Changes
}

func NewHandler(
	uow unitofwork.UnitOfWork,
	inbox unitofwork.Inbox,
	orders order.Repository,
	workflow fulfillment.Notifier,
	assignments order.CourierAssignments,
	changes syncorderrelationships.Changes,
) Handler {
	return Handler{
		reflector:   fulfillment.NewReflector(uow, inbox, orders, workflow),
		assignments: assignments,
		changes:     changes,
	}
}

func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	signal := fulfillment.InboundSignal{
		Consumer:   Consumer,
		EventID:    in.EventID,
		OrderID:    in.OrderID,
		Kind:       fulfillment.KindCourierAssigned,
		OccurredAt: in.OccurredAt,
	}
	// The assignment moves no State. A later event of the same Order is a
	// reassignment and replaces the assignment, its expected pickup time and,
	// for another Delivery, order#delivery; the Order is saved, so order_details
	// and the Order Change Notification follow the assignment.
	reflection, err := h.reflector.ReflectDetails(ctx, signal, func(current *order.Order) error {
		return current.AdmitCourierAssignment(in.OccurredAt)
	}, func(ctx context.Context, current *order.Order) (bool, error) {
		superseded, err := h.assign(ctx, in)
		if err != nil || !superseded {
			return false, err
		}
		expected, err := current.ExpectPickupAt(in.OccurredAt, in.ExpectedPickupAt)
		return expected || in.DeliveryID != "" && in.CourierID != "", err
	})
	return Result(reflection), err
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
