package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
)

// RememberRevision stores the ZedToken of the creation-time relationship in the
// Order row, inside the placement transaction (ADR 18).
func (s *Store) RememberRevision(ctx context.Context, orderID order.ID, revision application.AuthorizationRevision) error {
	tag, err := s.q(ctx).Exec(ctx, `UPDATE orders SET authorization_revision = $2 WHERE id = $1`, orderID, string(revision))
	if err != nil {
		return fmt.Errorf("remember authorization revision of %s: %w", orderID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	return nil
}

// Revision returns the stored authorization revision of an Order, if any.
func (s *Store) Revision(ctx context.Context, orderID string) (application.AuthorizationRevision, bool, error) {
	var revision *string
	err := s.q(ctx).QueryRow(ctx, `SELECT authorization_revision FROM orders WHERE id = $1`, orderID).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && revision == nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read authorization revision of %s: %w", orderID, err)
	}
	return application.AuthorizationRevision(*revision), true, nil
}
