// Package orderdetails is the OrderDetails read model of the order slice: the
// order card that Get Order and List Customer Orders return (ADR orders 4).
// Storage keeps it as a projection updated in the transaction of every Order
// change, so queries read it without restoring the Order aggregate.
package orderdetails

import (
	"context"
	"time"

	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// Details is one row of the projection.
type Details struct {
	OrderID      order.ID
	CustomerID   order.CustomerID
	MarketID     vo.MarketID
	RestaurantID order.RestaurantID
	Items        []Item
	// Service is the priority with the premium fee fixed at Place Order
	// (FR-650); the fee is part of Total.
	Service  order.Service
	Total    vo.Money
	State    order.State
	PlacedAt time.Time
	// Transitions is the history of the Order FSM.
	Transitions []order.AppliedTransition
	// CourierAssignedAt is the time of the current Courier Assignment; zero
	// without a courier.
	CourierAssignedAt time.Time
	// Expected are the latest named Fulfillment deadlines; a zero time is not named.
	Expected order.ExpectedTimes
	// DeliveryWindow is set for a Scheduled Order (FR-681).
	DeliveryWindow order.DeliveryWindow
	// Cancellation is nil unless the Order is cancelled.
	Cancellation *Cancellation
}

// Item is an order line with the Menu Item name kept at Place Order; Name is
// empty for Orders placed before the name was kept.
type Item struct {
	MenuItemID order.MenuItemID
	Name       string
	Quantity   int
	UnitPrice  vo.Money
}

type Cancellation struct {
	Outcome     order.NegativeOutcome
	ReasonCode  string
	CancelledAt time.Time
}

// View is the card returned to the reader: the projection row and the
// Cancellation Window of the moment of reading.
type View struct {
	Details
	// CancellationWindow is nil when the Order State does not admit Order
	// Cancellation or the policy could not supply the window.
	CancellationWindow *order.CancellationWindow
}

// CancellationPolicy is the policy Request Order Cancellation checks against;
// the view publishes the same window.
type CancellationPolicy interface {
	Window(ctx context.Context, placedAt time.Time, delivery order.DeliveryWindow) (order.CancellationWindow, error)
}

// Present adds the Cancellation Window to the row. The window is advisory: the
// authoritative check is Request Order Cancellation, so a failing policy hides
// the window instead of failing the read. The policy adapter reports its own
// failures.
func Present(ctx context.Context, policy CancellationPolicy, details Details) View {
	view := View{Details: details}
	if !details.State.AcceptsCancellation() {
		return view
	}
	window, err := policy.Window(ctx, details.PlacedAt, details.DeliveryWindow)
	if err != nil {
		return view
	}
	view.CancellationWindow = &window
	return view
}
