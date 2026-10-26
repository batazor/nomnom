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
// orders carries the full card of every Order, restaurants the restaurant of
// each distinct restaurant of the page.
type ListCustomerOrders struct {
	history OrderHistory
	catalog Catalog
	now     func() time.Time
}

func NewListCustomerOrders(history OrderHistory, catalog Catalog) ListCustomerOrders {
	return ListCustomerOrders{history: history, catalog: catalog, now: time.Now}
}

// Handle builds the page with one restaurants read per distinct restaurant.
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
	now := h.now()
	restaurants := map[string]Restaurant{}
	for _, entry := range page.Entries {
		restaurant, known := restaurants[entry.Order.RestaurantID]
		if !known {
			if restaurant, err = h.catalog.Restaurant(ctx, entry.Order.RestaurantID); err != nil {
				return OrderPage{}, fmt.Errorf("read restaurant: %w", err)
			}
			restaurants[entry.Order.RestaurantID] = restaurant
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
