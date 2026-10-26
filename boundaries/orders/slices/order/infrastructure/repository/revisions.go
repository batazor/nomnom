package repository

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
)

// RememberRevision stores the ZedToken of the last relationship sync of the
// Order in its row (ADR 18): permission checks of the Order read at least as
// fresh as it.
func (s *Store) RememberRevision(ctx context.Context, orderID order.ID, revision application.AuthorizationRevision) error {
	tag, err := s.exec(ctx, psql.Update("orders").
		Set("authorization_revision", string(revision)).Where(sq.Eq{"id": orderID}))
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
	err := s.queryRow(ctx, psql.Select("authorization_revision").From("orders").Where(sq.Eq{"id": orderID}), &revision)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && revision == nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read authorization revision of %s: %w", orderID, err)
	}
	return application.AuthorizationRevision(*revision), true, nil
}
