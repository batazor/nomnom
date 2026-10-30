package applications

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidPageSize is a page size outside 1..MaxPageSize.
	ErrInvalidPageSize = errors.New("page size must be between 1 and 100")
	// ErrInvalidCursor is a cursor orders does not recognize.
	ErrInvalidCursor = errors.New("cursor is invalid")
)

// MaxPageSize is the largest page of the order history orders serves.
const MaxPageSize = 100

// HistoryEntry is one Order of the history with its full card; Cursor
// continues after it.
type HistoryEntry struct {
	Order  OrderSnapshot
	Cursor string
}

// HistoryPage is one page of the history as orders returns it, newest first;
// NextCursor is empty on the last page.
type HistoryPage struct {
	Entries    []HistoryEntry
	NextCursor string
}

// OrderHistory pages the Orders of the customer in the request context; a
// cursor orders rejects is ErrInvalidCursor.
type OrderHistory interface {
	ListCustomerOrders(ctx context.Context, customerID string, size int, cursor string) (HistoryPage, error)
}

// OrderEdge is an Order with its cursor.
type OrderEdge struct {
	Cursor string
	Order  OrderView
}

// OrderPage is a page of the customerOrders connection.
type OrderPage struct {
	Edges       []OrderEdge
	HasNextPage bool
	// EndCursor is the cursor of the last edge, empty for an empty page.
	EndCursor string
}

// ListCustomerOrders pages the history of the customer: the history of
// orders carries the full card of every Order, one BatchGetRestaurants of
// restaurants the distinct restaurants of the page (ADR 7).
type ListCustomerOrders struct {
	history OrderHistory
	catalog Catalog
	now     func() time.Time
}

func NewListCustomerOrders(history OrderHistory, catalog Catalog) ListCustomerOrders {
	return ListCustomerOrders{history: history, catalog: catalog, now: time.Now}
}

// Handle builds the page with one restaurants read for all its restaurants.
func (h ListCustomerOrders) Handle(ctx context.Context, customerID string, first int, after string) (OrderPage, error) {
	if first < 1 || first > MaxPageSize {
		return OrderPage{}, ErrInvalidPageSize
	}
	page, err := h.history.ListCustomerOrders(ctx, customerID, first, after)
	if err != nil {
		return OrderPage{}, err
	}
	result := OrderPage{
		Edges:       make([]OrderEdge, 0, len(page.Entries)),
		HasNextPage: page.NextCursor != "",
	}
	restaurants, err := h.restaurantsOf(ctx, page.Entries)
	if err != nil {
		return OrderPage{}, err
	}
	now := h.now()
	for _, entry := range page.Entries {
		restaurant, found := restaurants[entry.Order.RestaurantID]
		if !found {
			return OrderPage{}, fmt.Errorf("read restaurant: restaurant %s of order %s not found", entry.Order.RestaurantID, entry.Order.ID)
		}
		view, err := compose(entry.Order, restaurant, now)
		if err != nil {
			return OrderPage{}, fmt.Errorf("compose order %s: %w", entry.Order.ID, err)
		}
		result.Edges = append(result.Edges, OrderEdge{Cursor: entry.Cursor, Order: view})
	}
	if len(result.Edges) > 0 {
		result.EndCursor = result.Edges[len(result.Edges)-1].Cursor
	}
	if page.NextCursor != "" {
		result.EndCursor = page.NextCursor
	}
	return result, nil
}

// restaurantsOf reads the distinct restaurants of the page in one call; an
// empty page reads nothing.
func (h ListCustomerOrders) restaurantsOf(ctx context.Context, entries []HistoryEntry) (map[string]Restaurant, error) {
	if len(entries) == 0 {
		return map[string]Restaurant{}, nil
	}
	seen := make(map[string]struct{}, len(entries))
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if _, ok := seen[entry.Order.RestaurantID]; ok {
			continue
		}
		seen[entry.Order.RestaurantID] = struct{}{}
		ids = append(ids, entry.Order.RestaurantID)
	}
	restaurants, err := h.catalog.Restaurants(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read restaurants: %w", err)
	}
	return restaurants, nil
}
