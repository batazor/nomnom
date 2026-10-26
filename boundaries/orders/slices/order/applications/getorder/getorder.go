// Package getorder returns the current view of one Order to an authorized actor.
package getorder

import (
	"context"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/domains/order"
)

const (
	PermissionView application.Permission   = "view"
	ResourceOrder  application.ResourceType = "order"
)

type Query struct {
	Subject application.Subject
	OrderID order.ID
}

type Authorizer interface {
	Check(ctx context.Context, check application.AccessCheck) error
}

// Reader reads the OrderDetails projection; it returns order.ErrOrderNotFound
// for a missing Order.
type Reader interface {
	OrderDetails(ctx context.Context, id order.ID) (orderdetails.Details, error)
}

type Handler struct {
	authorizer Authorizer
	reader     Reader
	policy     orderdetails.CancellationPolicy
}

func NewHandler(authorizer Authorizer, reader Reader, policy orderdetails.CancellationPolicy) Handler {
	return Handler{authorizer: authorizer, reader: reader, policy: policy}
}

// Handle authorizes before reading, so a denied subject learns nothing about
// whether the Order exists.
func (h Handler) Handle(ctx context.Context, q Query) (orderdetails.View, error) {
	if err := q.Subject.Validate(); err != nil {
		return orderdetails.View{}, err
	}
	if q.OrderID == "" {
		return orderdetails.View{}, application.InvalidInput("order_id", "is required")
	}
	err := h.authorizer.Check(ctx, application.AccessCheck{
		Subject:    q.Subject,
		Permission: PermissionView,
		Resource:   application.Resource{Type: ResourceOrder, ID: string(q.OrderID)},
	})
	if err := application.FailClosed(err); err != nil {
		return orderdetails.View{}, err
	}
	details, err := h.reader.OrderDetails(ctx, q.OrderID)
	if err != nil {
		return orderdetails.View{}, err
	}
	return orderdetails.Present(ctx, h.policy, details), nil
}
