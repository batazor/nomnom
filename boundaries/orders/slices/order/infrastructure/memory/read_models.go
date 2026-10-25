package memory

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/domains/order"
)

func (s *Store) OrderView(ctx context.Context, id order.ID) (getorder.OrderView, error) {
	defer s.lock(ctx)()
	snapshot, ok := s.state.orders[id]
	if !ok {
		return getorder.OrderView{}, fmt.Errorf("%w: %q", order.ErrOrderNotFound, id)
	}
	restored, err := order.Restore(snapshot)
	if err != nil {
		return getorder.OrderView{}, err
	}
	view := getorder.OrderView{
		OrderID:         snapshot.ID,
		CustomerID:      snapshot.CustomerID,
		RestaurantID:    snapshot.RestaurantID,
		DeliveryAddress: snapshot.DeliveryAddress.String(),
		Total:           restored.Total(),
		State:           snapshot.State,
		PlacedAt:        snapshot.PlacedAt,
	}
	for _, item := range snapshot.Items {
		view.Items = append(view.Items, getorder.ItemView{
			MenuItemID: item.MenuItemID(),
			Quantity:   item.Quantity().Value(),
			UnitPrice:  item.UnitPrice(),
		})
	}
	for _, fact := range snapshot.Facts {
		view.Facts = append(view.Facts, getorder.FactView{Fact: fact.Fact, OccurredAt: fact.OccurredAt, Reason: fact.Reason})
	}
	if !snapshot.CancellationReason.IsZero() {
		view.Cancellation = &getorder.CancellationView{
			Outcome:     snapshot.CancellationReason.Outcome(),
			ReasonCode:  snapshot.CancellationReason.Code(),
			CancelledAt: snapshot.CancelledAt,
		}
	}
	return view, nil
}

// ListCustomerOrders orders by placement time descending, then by id; the
// cursor is the offset of the next page.
func (s *Store) ListCustomerOrders(ctx context.Context, request listcustomerorders.PageRequest) (listcustomerorders.Page, error) {
	defer s.lock(ctx)()
	offset := 0
	if request.Cursor != "" {
		parsed, err := strconv.Atoi(request.Cursor)
		if err != nil || parsed < 0 {
			return listcustomerorders.Page{}, application.InvalidInput("cursor", "is malformed")
		}
		offset = parsed
	}

	var rows []listcustomerorders.OrderSummary
	for _, snapshot := range s.state.orders {
		if snapshot.CustomerID != request.CustomerID {
			continue
		}
		restored, err := order.Restore(snapshot)
		if err != nil {
			return listcustomerorders.Page{}, err
		}
		rows = append(rows, listcustomerorders.OrderSummary{
			OrderID:      snapshot.ID,
			RestaurantID: snapshot.RestaurantID,
			State:        snapshot.State,
			Total:        restored.Total(),
			PlacedAt:     snapshot.PlacedAt,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].PlacedAt.Equal(rows[j].PlacedAt) {
			return rows[i].PlacedAt.After(rows[j].PlacedAt)
		}
		return rows[i].OrderID > rows[j].OrderID
	})

	page := listcustomerorders.Page{Orders: []listcustomerorders.OrderSummary{}}
	if offset >= len(rows) {
		return page, nil
	}
	end := min(offset+request.Limit, len(rows))
	page.Orders = rows[offset:end]
	if end < len(rows) {
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

// CountByState — число Orders в каждом состоянии.
func (s *Store) CountByState(ctx context.Context) (map[order.State]int64, error) {
	defer s.lock(ctx)()
	counts := map[order.State]int64{}
	for _, snapshot := range s.state.orders {
		counts[snapshot.State]++
	}
	return counts, nil
}
