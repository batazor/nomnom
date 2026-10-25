package observability

import (
	"context"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
)

// Outbox is the Outbox port of the command use cases.
type Outbox interface {
	Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error
}

// ObservedStore decorates the Unit of Work and the Outbox of a process: after
// the commit of the outer Unit of Work it records the business metrics of the
// Domain Events that reached the Outbox (DomainMetrics). Nil Metrics is a
// no-op.
type ObservedStore struct {
	UnitOfWork application.UnitOfWork
	Outbox     Outbox
	Metrics    *DomainMetrics
}

func (s ObservedStore) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	ctx, done := s.Metrics.Track(ctx)
	err := s.UnitOfWork.Do(ctx, fn)
	done(err)
	return err
}

func (s ObservedStore) Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error {
	if err := s.Outbox.Append(ctx, aggregateID, aggregateVersion, events); err != nil {
		return err
	}
	s.Metrics.Appended(ctx, events)
	return nil
}
