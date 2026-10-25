package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// ErrIdempotencyKeyTaken: the key already identifies another Order.
var ErrIdempotencyKeyTaken = errors.New("idempotency key already identifies another order")

// Accept records (consumer, event_id) in the active transaction; false means
// the event was already processed (ADR 11).
func (s *Store) Accept(ctx context.Context, consumer, eventID string) (bool, error) {
	tag, err := s.q(ctx).Exec(ctx, `
		INSERT INTO inbox (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, consumer, eventID)
	if err != nil {
		return false, fmt.Errorf("accept inbox %s/%s: %w", consumer, eventID, err)
	}
	return tag.RowsAffected() == 1, nil
}

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

func (s *Store) OrderView(ctx context.Context, id order.ID) (getorder.OrderView, error) {
	snap, err := s.load(ctx, id)
	if err != nil {
		return getorder.OrderView{}, err
	}
	restored, err := order.Restore(snap)
	if err != nil {
		return getorder.OrderView{}, err
	}
	view := getorder.OrderView{
		OrderID:         snap.ID,
		CustomerID:      snap.CustomerID,
		RestaurantID:    snap.RestaurantID,
		DeliveryAddress: snap.DeliveryAddress.String(),
		Total:           restored.Total(),
		State:           snap.State,
		PlacedAt:        snap.PlacedAt,
	}
	for _, item := range snap.Items {
		view.Items = append(view.Items, getorder.ItemView{
			MenuItemID: item.MenuItemID(), Quantity: item.Quantity().Value(), UnitPrice: item.UnitPrice(),
		})
	}
	for _, fact := range snap.Facts {
		view.Facts = append(view.Facts, getorder.FactView{Fact: fact.Fact, OccurredAt: fact.OccurredAt, Reason: fact.Reason})
	}
	if !snap.CancellationReason.IsZero() {
		view.Cancellation = &getorder.CancellationView{
			Outcome:     snap.CancellationReason.Outcome(),
			ReasonCode:  snap.CancellationReason.Code(),
			CancelledAt: snap.CancelledAt,
		}
	}
	return view, nil
}

// ListCustomerOrders pages by (placed_at desc, id desc) with a keyset cursor.
func (s *Store) ListCustomerOrders(ctx context.Context, request listcustomerorders.PageRequest) (listcustomerorders.Page, error) {
	args := []any{request.CustomerID, request.Limit + 1}
	where := ""
	if request.Cursor != "" {
		placedAt, id, err := decodeCursor(request.Cursor)
		if err != nil {
			return listcustomerorders.Page{}, application.InvalidInput("cursor", "is malformed")
		}
		where = " AND (placed_at, id) < ($3, $4)"
		args = append(args, placedAt, id)
	}
	rows, err := s.q(ctx).Query(ctx, `
		SELECT id, restaurant_id, state, total_amount::text, total_currency, placed_at
		FROM orders WHERE customer_id = $1`+where+`
		ORDER BY placed_at DESC, id DESC LIMIT $2`, args...)
	if err != nil {
		return listcustomerorders.Page{}, fmt.Errorf("list orders of %s: %w", request.CustomerID, err)
	}
	defer rows.Close()

	page := listcustomerorders.Page{Orders: []listcustomerorders.OrderSummary{}}
	for rows.Next() {
		var (
			summary          listcustomerorders.OrderSummary
			amount, currency string
		)
		if err := rows.Scan(&summary.OrderID, &summary.RestaurantID, &summary.State, &amount, &currency, &summary.PlacedAt); err != nil {
			return listcustomerorders.Page{}, fmt.Errorf("scan order summary: %w", err)
		}
		if summary.Total, err = vo.ParseMoney(amount, currency); err != nil {
			return listcustomerorders.Page{}, fmt.Errorf("order %s total: %w", summary.OrderID, err)
		}
		summary.PlacedAt = summary.PlacedAt.UTC()
		page.Orders = append(page.Orders, summary)
	}
	if err := rows.Err(); err != nil {
		return listcustomerorders.Page{}, err
	}
	if len(page.Orders) > request.Limit {
		page.Orders = page.Orders[:request.Limit]
		last := page.Orders[len(page.Orders)-1]
		page.NextCursor = encodeCursor(last.PlacedAt, string(last.OrderID))
	}
	return page, nil
}

func encodeCursor(placedAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(placedAt.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	stamp, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return time.Time{}, "", errors.New("cursor without id")
	}
	placedAt, err := time.Parse(time.RFC3339Nano, stamp)
	return placedAt, id, err
}

// OrderCustomer is the business association reconciled with order#customer.
type OrderCustomer struct {
	OrderID    string
	CustomerID string
}

// CustomersOf returns the customer of each existing Order among ids.
func (s *Store) CustomersOf(ctx context.Context, ids []string) (map[string]string, error) {
	rows, err := s.q(ctx).Query(ctx, `SELECT id, customer_id FROM orders WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("read customers of orders: %w", err)
	}
	defer rows.Close()
	customers := make(map[string]string, len(ids))
	for rows.Next() {
		var id, customer string
		if err := rows.Scan(&id, &customer); err != nil {
			return nil, fmt.Errorf("scan order customer: %w", err)
		}
		customers[id] = customer
	}
	return customers, rows.Err()
}

// OrderCustomerPage pages Orders by id after afterID.
func (s *Store) OrderCustomerPage(ctx context.Context, afterID string, limit int) ([]OrderCustomer, error) {
	rows, err := s.q(ctx).Query(ctx, `
		SELECT id, customer_id FROM orders WHERE id > $1 ORDER BY id LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("page orders: %w", err)
	}
	defer rows.Close()
	var page []OrderCustomer
	for rows.Next() {
		var row OrderCustomer
		if err := rows.Scan(&row.OrderID, &row.CustomerID); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		page = append(page, row)
	}
	return page, rows.Err()
}
