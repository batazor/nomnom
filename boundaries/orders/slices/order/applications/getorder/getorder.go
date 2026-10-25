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

type Handler struct {
	authorizer Authorizer
	reader     Reader
}

func NewHandler(authorizer Authorizer, reader Reader) Handler {
	return Handler{authorizer: authorizer, reader: reader}
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
	return h.reader.OrderView(ctx, q.OrderID)
}
