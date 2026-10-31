// Package syncrelationships brings the SpiceDB relationships of an Order
// (order#customer, order#restaurant, order#delivery) to the current state of
// the Order in PostgreSQL (ADR 18). Use cases that change these subjects do
// not write SpiceDB: they record RelationshipsChanged in the Outbox of their
// transaction, and orders-consumer runs this use case for it. The sync reads
// the state, not the event, so a repeated, late or redriven event converges
// to the same relationships.
package syncrelationships

import (
	"context"
	"fmt"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/domains/order"
)

// Changes records in the active transaction that the relationships of an
// Order must be synced; the record commits or rolls back with the change.
type Changes interface {
	RelationshipsChanged(ctx context.Context, orderID order.ID) error
}

// Subjects are the subjects of the Order relations as PostgreSQL stores them.
// DeliveryID is empty when the Order has no Delivery: order#delivery is absent.
type Subjects struct {
	CustomerID   order.CustomerID
	RestaurantID order.RestaurantID
	DeliveryID   string
}

// Reader reads the Subjects of an Order and locks its row for the active
// transaction, so a change of the subjects commits after the sync and records
// a new RelationshipsChanged; it returns order.ErrOrderNotFound for a missing
// Order.
type Reader interface {
	RelationshipSubjects(ctx context.Context, orderID order.ID) (Subjects, error)
}

// Relationships makes the order relationships in SpiceDB exactly the
// Subjects in one atomic write and returns its revision.
type Relationships interface {
	SyncOrderRelationships(ctx context.Context, orderID order.ID, subjects Subjects) (authz.AuthorizationRevision, error)
}

// Revisions stores the revision with the Order: later permission checks of
// the Order read at least as fresh as the sync.
type Revisions interface {
	RememberRevision(ctx context.Context, orderID order.ID, revision authz.AuthorizationRevision) error
}

type Input struct {
	OrderID order.ID
}

type Result struct {
	Revision authz.AuthorizationRevision
}

type Handler struct {
	uow           unitofwork.UnitOfWork
	reader        Reader
	relationships Relationships
	revisions     Revisions
}

func NewHandler(uow unitofwork.UnitOfWork, reader Reader, relationships Relationships, revisions Revisions) Handler {
	return Handler{uow: uow, reader: reader, relationships: relationships, revisions: revisions}
}

// Handle runs UoW { read the subjects under a row lock → write SpiceDB →
// remember the revision }. A failed SpiceDB write rolls nothing forward: the
// event is redelivered and the sync repeats.
func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	if in.OrderID == "" {
		return Result{}, usecase.InvalidInput("order_id", "is required")
	}
	var result Result
	err := h.uow.Do(ctx, func(ctx context.Context) error {
		subjects, err := h.reader.RelationshipSubjects(ctx, in.OrderID)
		if err != nil {
			return err
		}
		revision, err := h.relationships.SyncOrderRelationships(ctx, in.OrderID, subjects)
		if err != nil {
			return fmt.Errorf("sync relationships of order %s: %w", in.OrderID, err)
		}
		if err := h.revisions.RememberRevision(ctx, in.OrderID, revision); err != nil {
			return fmt.Errorf("remember authorization revision: %w", err)
		}
		result.Revision = revision
		return nil
	})
	return result, err
}
