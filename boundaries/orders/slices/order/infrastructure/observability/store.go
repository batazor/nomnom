package observability

import (
	"context"

	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/slices/order/domains/order"
)

// Outbox is the Outbox port of the command use cases.
type Outbox interface {
	Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error
}

// ObservedStore decorates the Unit of Work, the Order repository and the Outbox
// of a process: after the commit of the outer Unit of Work it records the
// business metrics of the Domain Events that reached the Outbox and the FSM
// transitions of the saved Orders (DomainMetrics). Every attempt of a retried
// transaction collects anew. Nil Metrics is a no-op.
type ObservedStore struct {
	UnitOfWork unitofwork.UnitOfWork
	Orders     order.Repository
	Outbox     Outbox
	Metrics    *DomainMetrics
}

func (s ObservedStore) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	ctx, attempt, done := s.Metrics.Track(ctx)
	err := s.UnitOfWork.Do(ctx, func(ctx context.Context) error {
		attempt()
		return fn(ctx)
	})
	done(err)
	return err
}

func (s ObservedStore) Get(ctx context.Context, id order.ID) (*order.Order, error) {
	current, err := s.Orders.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.Metrics.Loaded(ctx, current)
	return current, nil
}

// Add creates an Order; creation is not an FSM transition.
func (s ObservedStore) Add(ctx context.Context, created *order.Order) error {
	return s.Orders.Add(ctx, created)
}

func (s ObservedStore) Save(ctx context.Context, changed *order.Order) error {
	if err := s.Orders.Save(ctx, changed); err != nil {
		return err
	}
	s.Metrics.Saved(ctx, changed)
	return nil
}

func (s ObservedStore) Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error {
	if err := s.Outbox.Append(ctx, aggregateID, aggregateVersion, events); err != nil {
		return err
	}
	s.Metrics.Appended(ctx, events)
	return nil
}
