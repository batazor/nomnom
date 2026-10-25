package applications

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrInvalidPageSize is a page size outside 1..MaxPageSize.
	ErrInvalidPageSize = errors.New("page size must be between 1 and 100")
	// ErrInvalidCursor is a cursor orders does not recognize.
	ErrInvalidCursor = errors.New("cursor is invalid")
)

// MaxPageSize is the largest page of the order history orders serves.
const MaxPageSize = 100

// HistoryEntry is one Order of the history; Cursor continues after it.
type HistoryEntry struct {
	OrderID string
	Cursor  string
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

// OrderViews builds the customer view of one Order: GetOrder.
type OrderViews interface {
	Handle(ctx context.Context, orderID string) (OrderView, error)
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

// ListCustomerOrders pages the history of the customer: the history read of
// orders gives the ids and cursors, GetOrder the full view of each Order,
// because the summary carries no items and no Fulfillment Facts.
type ListCustomerOrders struct {
	history OrderHistory
	orders  OrderViews
}

func NewListCustomerOrders(history OrderHistory, orders OrderViews) ListCustomerOrders {
	return ListCustomerOrders{history: history, orders: orders}
}

// Handle reads the Orders of a page one by one; an Order that disappears
// between the two reads is left out.
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
	for _, entry := range page.Entries {
		view, err := h.orders.Handle(ctx, entry.OrderID)
		if errors.Is(err, ErrOrderNotFound) {
			continue
		}
		if err != nil {
			return OrderPage{}, fmt.Errorf("read order %s: %w", entry.OrderID, err)
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
