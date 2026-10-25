package memory

import (
	"context"
	"fmt"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
)

func (s *Store) Get(ctx context.Context, id order.ID) (*order.Order, error) {
	defer s.lock(ctx)()
	snapshot, ok := s.state.orders[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", order.ErrOrderNotFound, id)
	}
	return order.Restore(snapshot)
}

func (s *Store) Add(ctx context.Context, created *order.Order) error {
	defer s.lock(ctx)()
	if _, exists := s.state.orders[created.ID()]; exists {
		return fmt.Errorf("%w: %q", order.ErrOrderAlreadyExists, created.ID())
	}
	s.state.orders[created.ID()] = persisted(created)
	return nil
}

func (s *Store) Save(ctx context.Context, changed *order.Order) error {
	defer s.lock(ctx)()
	stored, exists := s.state.orders[changed.ID()]
	if !exists {
		return fmt.Errorf("%w: %q", order.ErrOrderNotFound, changed.ID())
	}
	if stored.Version != changed.Version() {
		return fmt.Errorf("%w: order %q has version %d, loaded %d",
			application.ErrConcurrencyConflict, changed.ID(), stored.Version, changed.Version())
	}
	s.state.orders[changed.ID()] = persisted(changed)
	return nil
}

func persisted(o *order.Order) order.Snapshot {
	snapshot := o.Snapshot()
	snapshot.Version = o.Version() + 1
	return snapshot
}
