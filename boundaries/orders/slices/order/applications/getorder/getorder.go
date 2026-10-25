// Package getorder returns the current view of one Order to an authorized actor.
package getorder

import (
	"context"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

const (
	PermissionView application.Permission   = "view"
	ResourceOrder  application.ResourceType = "order"
)

type Query struct {
	Subject application.Subject
	OrderID order.ID
}

// OrderView is the read model of this query and its Application DTO.
type OrderView struct {
	OrderID         order.ID
	CustomerID      order.CustomerID
	RestaurantID    order.RestaurantID
	Items           []ItemView
	DeliveryAddress string
	Total           vo.Money
	State           order.State
	PlacedAt        time.Time
	Facts           []FactView
	// Cancellation is nil unless the Order is cancelled.
	Cancellation *CancellationView
	// CancellationWindow is nil when the Order State does not admit Order
	// Cancellation or the policy could not supply the window.
	CancellationWindow *order.CancellationWindow
}

type ItemView struct {
	MenuItemID order.MenuItemID
	Quantity   int
	UnitPrice  vo.Money
}

type FactView struct {
	Fact       order.FulfillmentFact
	OccurredAt time.Time
	Reason     string
}

type CancellationView struct {
	Outcome     order.NegativeOutcome
	ReasonCode  string
	CancelledAt time.Time
}

type Authorizer interface {
	Check(ctx context.Context, check application.AccessCheck) error
}

// Reader returns order.ErrOrderNotFound for a missing Order.
type Reader interface {
	OrderView(ctx context.Context, id order.ID) (OrderView, error)
}

// CancellationPolicy is the policy Request Order Cancellation checks against;
// the view publishes the same window.
type CancellationPolicy interface {
	Window(ctx context.Context, placedAt time.Time) (order.CancellationWindow, error)
}

type Handler struct {
	authorizer Authorizer
	reader     Reader
	policy     CancellationPolicy
}

func NewHandler(authorizer Authorizer, reader Reader, policy CancellationPolicy) Handler {
	return Handler{authorizer: authorizer, reader: reader, policy: policy}
}

// Handle authorizes before reading, so a denied subject learns nothing about
// whether the Order exists.
func (h Handler) Handle(ctx context.Context, q Query) (OrderView, error) {
	if err := q.Subject.Validate(); err != nil {
		return OrderView{}, err
	}
	if q.OrderID == "" {
		return OrderView{}, application.InvalidInput("order_id", "is required")
	}
	err := h.authorizer.Check(ctx, application.AccessCheck{
		Subject:    q.Subject,
		Permission: PermissionView,
		Resource:   application.Resource{Type: ResourceOrder, ID: string(q.OrderID)},
	})
	if err := application.FailClosed(err); err != nil {
		return OrderView{}, err
	}
	view, err := h.reader.OrderView(ctx, q.OrderID)
	if err != nil {
		return OrderView{}, err
	}
	view.CancellationWindow = h.cancellationWindow(ctx, view)
	return view, nil
}

// cancellationWindow is advisory: the authoritative check is Request Order
// Cancellation, so a failing policy hides the window instead of failing the
// read. The policy adapter reports its own failures.
func (h Handler) cancellationWindow(ctx context.Context, view OrderView) *order.CancellationWindow {
	if !view.State.AcceptsCancellation() {
		return nil
	}
	window, err := h.policy.Window(ctx, view.PlacedAt)
	if err != nil {
		return nil
	}
	return &window
}
