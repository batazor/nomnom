// Package getfulfillmentorder returns to the Activities of the Order Fulfillment
// Workflow the data of the stored Order that requests to neighbouring contexts
// are built from. It reads the Order itself, not the OrderDetails projection, so
// an Activity never sees a stale copy; the caller is the worker service identity,
// not a Customer, so there is no permission check.
package getfulfillmentorder

import (
	"context"
	"time"

	"nomnom/orders/slices/order/domains/order"
)

type Query struct {
	OrderID order.ID
}

type Item struct {
	MenuItemID string
	Quantity   int
}

// DeliveryWindow is the Delivery Window of a Scheduled Order.
type DeliveryWindow struct {
	Start time.Time
	End   time.Time
}

// View holds no domain types: the fulfillment slice does not depend on the Order
// aggregate. DeliveryWindow is nil for an ASAP Order.
type View struct {
	OrderID        string
	CustomerID     string
	RestaurantID   string
	MarketID       string
	Amount         string
	Currency       string
	Items          []Item
	Priority       string
	DeliveryWindow *DeliveryWindow
}

// Orders reads the stored Order; it returns order.ErrOrderNotFound for a missing
// one.
type Orders interface {
	Get(ctx context.Context, id order.ID) (*order.Order, error)
}

type Handler struct {
	orders Orders
}

func NewHandler(orders Orders) Handler {
	return Handler{orders: orders}
}

func (h Handler) Handle(ctx context.Context, q Query) (View, error) {
	current, err := h.orders.Get(ctx, q.OrderID)
	if err != nil {
		return View{}, err
	}
	items := make([]Item, 0, len(current.Items()))
	for _, item := range current.Items() {
		items = append(items, Item{MenuItemID: string(item.MenuItemID()), Quantity: item.Quantity().Value()})
	}
	view := View{
		OrderID:      string(current.ID()),
		CustomerID:   string(current.CustomerID()),
		RestaurantID: string(current.RestaurantID()),
		MarketID:     current.MarketID().String(),
		Amount:       current.Total().Amount().String(),
		Currency:     current.Total().Currency(),
		Items:        items,
		Priority:     string(current.Service().Priority()),
	}
	if current.Scheduled() {
		view.DeliveryWindow = &DeliveryWindow{Start: current.DeliveryWindow().Start(), End: current.DeliveryWindow().End()}
	}
	return view, nil
}
