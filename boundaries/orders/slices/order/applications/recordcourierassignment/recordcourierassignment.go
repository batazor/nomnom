// Package recordcourierassignment records Courier Assignment for the Delivery
// of an Order and keeps the relationship order#delivery in the same Inbox unit
// of work. The courier sees the Order through delivery->act_as_courier, which
// delivery owns; orders keeps the assignment as business data.
package recordcourierassignment

import (
	"context"
	"fmt"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
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
	// records the fact without relationships.
	DeliveryID string
	CourierID  string
}

type Result struct {
	Outcome          fulfillment.Outcome
	WorkflowNotified bool
}

// Relationships writes order#delivery of deliveryID and deletes that of
// displacedDeliveryID (when set) in one atomic request, returning the revision
// of the write. Repeating the request is idempotent.
type Relationships interface {
	LinkDelivery(ctx context.Context, orderID order.ID, deliveryID, displacedDeliveryID string) (application.AuthorizationRevision, error)
}

type Handler struct {
	reflector     fulfillment.Reflector
	assignments   order.CourierAssignments
	relationships Relationships
}

func NewHandler(
	uow application.UnitOfWork,
	inbox application.Inbox,
	orders order.Repository,
	workflow fulfillment.Notifier,
	assignments order.CourierAssignments,
	relationships Relationships,
) Handler {
	return Handler{
		reflector:     fulfillment.NewReflector(uow, inbox, orders, workflow),
		assignments:   assignments,
		relationships: relationships,
	}
}

func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	fact := fulfillment.InboundFact{
		Consumer:   Consumer,
		EventID:    in.EventID,
		OrderID:    in.OrderID,
		Kind:       fulfillment.KindCourierAssigned,
		OccurredAt: in.OccurredAt,
	}
	// The fact is recorded once; a later event of the same Order is a
	// reassignment and only replaces the assignment and, for another
	// Delivery, order#delivery.
	reflection, err := h.reflector.ReflectWith(ctx, fact, func(current *order.Order) error {
		_, err := current.RecordCourierAssignment(in.OccurredAt)
		return err
	}, func(ctx context.Context, _ *order.Order, _ fulfillment.Outcome) error {
		return h.assign(ctx, in)
	})
	return Result(reflection), err
}

// assign runs before the Inbox commit: a failed relationship write rolls the
// event back and redelivery repeats it. Whether the event supersedes the
// stored assignment and which Delivery it links or displaces is decided by
// order.CourierAssignment; the use case only writes and stores the result. A
// reassignment within the linked Delivery writes nothing to SpiceDB and keeps
// the stored revision.
func (h Handler) assign(ctx context.Context, in Input) error {
	if in.DeliveryID == "" || in.CourierID == "" {
		return nil
	}
	next := order.CourierAssignment{DeliveryID: in.DeliveryID, CourierID: in.CourierID, AssignedAt: in.OccurredAt, DeliveryVersion: in.DeliveryVersion}
	stored, found, err := h.assignments.CourierAssignment(ctx, in.OrderID)
	if err != nil {
		return fmt.Errorf("read courier assignment: %w", err)
	}
	if found && !next.Supersedes(stored) {
		return nil
	}
	var revision application.AuthorizationRevision
	if !found || !next.LinksDelivery(stored) {
		revision, err = h.relationships.LinkDelivery(ctx, in.OrderID, next.DeliveryID, next.DisplacedDelivery(stored))
		if err != nil {
			return fmt.Errorf("write order#delivery: %w", err)
		}
	}
	if err := h.assignments.RememberCourierAssignment(ctx, in.OrderID, next, string(revision)); err != nil {
		return fmt.Errorf("remember courier assignment: %w", err)
	}
	return nil
}
