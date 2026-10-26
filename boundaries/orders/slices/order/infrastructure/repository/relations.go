package repository

import (
	"context"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/domains/order"
)

// RelationshipSubjects reads the subjects of the Order relations in SpiceDB
// (ADR 18) — customer_id, restaurant_id, delivery_id — under FOR SHARE of the
// row: Place Order, Record Courier Assignment and Record Courier Release
// change them under FOR UPDATE and wait for the sync to commit, then record
// RelationshipsChanged again.
func (s *Store) RelationshipSubjects(ctx context.Context, orderID order.ID) (syncorderrelationships.Subjects, error) {
	var (
		subjects   syncorderrelationships.Subjects
		deliveryID *string
	)
	err := s.queryRow(ctx, psql.Select("customer_id", "restaurant_id", "delivery_id").
		From("orders").Where(sq.Eq{"id": orderID}).Suffix("FOR SHARE"),
		&subjects.CustomerID, &subjects.RestaurantID, &deliveryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return syncorderrelationships.Subjects{}, fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	if err != nil {
		return syncorderrelationships.Subjects{}, fmt.Errorf("read relationship subjects of %s: %w", orderID, err)
	}
	subjects.DeliveryID = deref(deliveryID)
	return subjects, nil
}

// OrderVersion is the stored version of the Order: the aggregate_version of
// the RelationshipsChanged notification of a change that keeps the version,
// such as a Courier Assignment.
func (s *Store) OrderVersion(ctx context.Context, orderID order.ID) (uint64, error) {
	var version uint64
	err := s.queryRow(ctx, psql.Select("version").From("orders").Where(sq.Eq{"id": orderID}), &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	if err != nil {
		return 0, fmt.Errorf("read version of %s: %w", orderID, err)
	}
	return version, nil
}

// OrderIDs pages the ids of all Orders after afterID: orders-admin authz
// resync syncs every Order with them.
func (s *Store) OrderIDs(ctx context.Context, afterID order.ID, limit int) ([]order.ID, error) {
	rows, err := s.query(ctx, psql.Select("id").From("orders").
		Where(sq.Gt{"id": afterID}).OrderBy("id").Limit(uint64(limit))) //nolint:gosec // limit is a positive page size.
	if err != nil {
		return nil, fmt.Errorf("page order ids: %w", err)
	}
	defer rows.Close()
	var ids []order.ID
	for rows.Next() {
		var id order.ID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan order id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
