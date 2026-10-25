package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
)

// ErrIdempotencyKeyTaken: the key already identifies another Order.
var ErrIdempotencyKeyTaken = placeorder.ErrIdempotencyKeyTaken

func (s *Store) FindOrder(ctx context.Context, customerID order.CustomerID, key string) (order.ID, bool, error) {
	var id order.ID
	err := s.q(ctx).QueryRow(ctx, `
		SELECT order_id FROM idempotency_keys WHERE customer_id = $1 AND key = $2`, customerID, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find idempotency key: %w", err)
	}
	return id, true, nil
}

// RememberOrder inserts the key in the active transaction. The unique index
// makes a concurrent insert of the same key wait for the first transaction;
// if that one committed another Order, ErrIdempotencyKeyTaken.
func (s *Store) RememberOrder(ctx context.Context, customerID order.CustomerID, key string, orderID order.ID) error {
	tag, err := s.q(ctx).Exec(ctx, `
		INSERT INTO idempotency_keys (customer_id, key, order_id) VALUES ($1, $2, $3)
		ON CONFLICT (customer_id, key) DO UPDATE SET order_id = EXCLUDED.order_id
		WHERE idempotency_keys.order_id = EXCLUDED.order_id`, customerID, key, orderID)
	if err != nil {
		return fmt.Errorf("remember idempotency key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrIdempotencyKeyTaken
	}
	return nil
}
