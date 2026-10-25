package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/domains/order"
)

// CourierAssignment reads the Courier Assignment of an Order and locks its row
// until the end of the active transaction, so reassignments of one Order apply
// one after another.
func (s *Store) CourierAssignment(ctx context.Context, orderID order.ID) (recordcourierassignment.Assignment, bool, error) {
	var (
		deliveryID, courierID *string
		assignedAt            *time.Time
		version               int64
	)
	err := s.q(ctx).QueryRow(ctx, `
		SELECT delivery_id, courier_id, courier_assigned_at, courier_delivery_version FROM orders WHERE id = $1 FOR UPDATE`, orderID,
	).Scan(&deliveryID, &courierID, &assignedAt, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return recordcourierassignment.Assignment{}, false, fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	if err != nil {
		return recordcourierassignment.Assignment{}, false, fmt.Errorf("read courier assignment of %s: %w", orderID, err)
	}
	// A released assignment keeps its time with an empty courier (and, for a
	// cancelled Delivery, an empty delivery): it still orders later events.
	if assignedAt == nil {
		return recordcourierassignment.Assignment{}, false, nil
	}
	return recordcourierassignment.Assignment{
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
func (s *Store) RememberCourierAssignment(ctx context.Context, orderID order.ID, assignment recordcourierassignment.Assignment, revision application.AuthorizationRevision) error {
	if assignment.DeliveryVersion > math.MaxInt64 {
		return fmt.Errorf("remember courier assignment of %s: delivery version %d overflows BIGINT", orderID, assignment.DeliveryVersion)
	}
	tag, err := s.q(ctx).Exec(ctx, `
		UPDATE orders
		SET delivery_id = NULLIF($2, ''), courier_id = NULLIF($3, ''), courier_assigned_at = $4,
		    courier_delivery_version = $5,
		    authorization_revision = COALESCE(NULLIF($6, ''), authorization_revision)
		WHERE id = $1`,
		orderID, assignment.DeliveryID, assignment.CourierID, assignment.AssignedAt,
		int64(assignment.DeliveryVersion), string(revision))
	if err != nil {
		return fmt.Errorf("remember courier assignment of %s: %w", orderID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	return nil
}
