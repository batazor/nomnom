package memory

import (
	"context"
	"errors"

	"nomnom/orders/slices/order/domains/order"
)

var ErrIdempotencyKeyTaken = errors.New("idempotency key already identifies another order")

func (s *Store) FindOrder(ctx context.Context, customerID order.CustomerID, key string) (order.ID, bool, error) {
	defer s.lock(ctx)()
	id, ok := s.state.idempotency[idempotencyKey{customerID: customerID, key: key}]
	return id, ok, nil
}

func (s *Store) RememberOrder(ctx context.Context, customerID order.CustomerID, key string, orderID order.ID) error {
	defer s.lock(ctx)()
	mapKey := idempotencyKey{customerID: customerID, key: key}
	if existing, ok := s.state.idempotency[mapKey]; ok && existing != orderID {
		return ErrIdempotencyKeyTaken
	}
	s.state.idempotency[mapKey] = orderID
	return nil
}
