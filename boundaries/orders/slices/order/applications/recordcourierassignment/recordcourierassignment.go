// Package recordcourierassignment records Courier Assignment for the Delivery
// of an Order and keeps the relationships order#courier and order#delivery of
// the assigned courier in the same Inbox unit of work.
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
	// DeliveryID and CourierID name the assignment; an event without them
	// records the fact without relationships.
	DeliveryID string
	CourierID  string
}

type Result struct {
	Outcome          fulfillment.Outcome
	WorkflowNotified bool
}

// Assignment is the Courier Assignment of an Order as reported by delivery.
// CourierID is a user id: the courier is checked as user:<id>.
type Assignment struct {
	DeliveryID string
	CourierID  string
	AssignedAt time.Time
}

// Assignments stores the current Assignment of an Order in the active
// transaction together with the revision of its relationship write.
type Assignments interface {
	CourierAssignment(ctx context.Context, orderID order.ID) (Assignment, bool, error)
	RememberCourierAssignment(ctx context.Context, orderID order.ID, assignment Assignment, revision application.AuthorizationRevision) error
}

// Relationships writes order#courier and order#delivery of next and deletes
// those of previous (when they differ) in one atomic request, returning the
// revision of the write. Repeating the request is idempotent.
type Relationships interface {
	AssignCourier(ctx context.Context, orderID order.ID, next Assignment, previous *Assignment) (application.AuthorizationRevision, error)
}

type Handler struct {
	reflector     fulfillment.Reflector
	assignments   Assignments
	relationships Relationships
}

func NewHandler(
	uow application.UnitOfWork,
	inbox application.Inbox,
	orders order.Repository,
	workflow fulfillment.Notifier,
	assignments Assignments,
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
	// reassignment and only replaces the relationships.
	reflection, err := h.reflector.ReflectWith(ctx, fact, func(current *order.Order) error {
		_, err := current.RecordCourierAssignment(in.OccurredAt)
		return err
	}, func(ctx context.Context, _ *order.Order, _ fulfillment.Outcome) error {
		return h.assign(ctx, in)
	})
	return Result(reflection), err
}

// assign runs before the Inbox commit: a failed relationship write rolls the
// event back and redelivery repeats it. An event older than the stored
// Assignment is out of order and changes nothing.
func (h Handler) assign(ctx context.Context, in Input) error {
	if in.DeliveryID == "" || in.CourierID == "" {
		return nil
	}
	next := Assignment{DeliveryID: in.DeliveryID, CourierID: in.CourierID, AssignedAt: in.OccurredAt}
	stored, found, err := h.assignments.CourierAssignment(ctx, in.OrderID)
	if err != nil {
		return fmt.Errorf("read courier assignment: %w", err)
	}
	var previous *Assignment
	if found {
		if stored.AssignedAt.After(next.AssignedAt) {
			return nil
		}
		previous = &stored
	}
	revision, err := h.relationships.AssignCourier(ctx, in.OrderID, next, previous)
	if err != nil {
		return fmt.Errorf("write courier relationships: %w", err)
	}
	if err := h.assignments.RememberCourierAssignment(ctx, in.OrderID, next, revision); err != nil {
		return fmt.Errorf("remember courier assignment: %w", err)
	}
	return nil
}
