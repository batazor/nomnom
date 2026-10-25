package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

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

// CountByState — число Orders в каждом состоянии для gauge nomnom.orders.in_state.
func (s *Store) CountByState(ctx context.Context) (map[order.State]int64, error) {
	rows, err := s.q(ctx).Query(ctx, `SELECT state, count(*) FROM orders GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("count orders by state: %w", err)
	}
	defer rows.Close()
	counts := map[order.State]int64{}
	for rows.Next() {
		var state string
		var n int64
		if err := rows.Scan(&state, &n); err != nil {
			return nil, fmt.Errorf("count orders by state: %w", err)
		}
		counts[order.State(state)] = n
	}
	return counts, rows.Err()
}
