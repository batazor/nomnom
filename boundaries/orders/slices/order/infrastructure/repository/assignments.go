package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"nomnom/orders/slices/order/domains/order"
)

// CourierAssignment reads the Courier Assignment of an Order and locks its row
// until the end of the active transaction, so reassignments of one Order apply
// one after another.
func (s *Store) CourierAssignment(ctx context.Context, orderID order.ID) (order.CourierAssignment, bool, error) {
	var (
		deliveryID, courierID *string
		assignedAt            *time.Time
		version               int64
	)
	err := s.queryRow(ctx, psql.
		Select("delivery_id", "courier_id", "courier_assigned_at", "courier_delivery_version").
		From("orders").Where(sq.Eq{"id": orderID}).Suffix("FOR UPDATE"),
		&deliveryID, &courierID, &assignedAt, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return order.CourierAssignment{}, false, fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	if err != nil {
		return order.CourierAssignment{}, false, fmt.Errorf("read courier assignment of %s: %w", orderID, err)
	}
	// A released assignment keeps its time with an empty courier (and, for a
	// cancelled Delivery, an empty delivery): it still orders later events.
	if assignedAt == nil {
		return order.CourierAssignment{}, false, nil
	}
	return order.CourierAssignment{
		DeliveryID: deref(deliveryID), CourierID: deref(courierID), AssignedAt: assignedAt.UTC(),
		DeliveryVersion: uint64(version), //nolint:gosec // the column is CHECK (>= 0).
	}, true, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// RememberCourierAssignment replaces the Courier Assignment of an Order and
// the ZedToken of its relationship write in the active transaction. An empty
// delivery or courier is stored as NULL, so the relation is absent for the
// reconciliation. The Delivery version is stored with it and orders later
// courier events.
func (s *Store) RememberCourierAssignment(ctx context.Context, orderID order.ID, assignment order.CourierAssignment, revision string) error {
	if assignment.DeliveryVersion > math.MaxInt64 {
		return fmt.Errorf("remember courier assignment of %s: delivery version %d overflows BIGINT", orderID, assignment.DeliveryVersion)
	}
	tag, err := s.exec(ctx, psql.Update("orders").
		Set("delivery_id", sq.Expr("NULLIF(?, '')", assignment.DeliveryID)).
		Set("courier_id", sq.Expr("NULLIF(?, '')", assignment.CourierID)).
		Set("courier_assigned_at", assignment.AssignedAt).
		Set("courier_delivery_version", int64(assignment.DeliveryVersion)).
		Set("authorization_revision", sq.Expr("COALESCE(NULLIF(?, ''), authorization_revision)", revision)).
		Where(sq.Eq{"id": orderID}))
	if err != nil {
		return fmt.Errorf("remember courier assignment of %s: %w", orderID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	return nil
}
