// Package getorder returns the current view of one Order to an authorized actor.
package getorder

import (
	"context"
	"errors"

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

// Handle lets the Customer read the own Order by customer_id: order#customer
// is written after the placement commit (ADR 18), and the Customer opens the
// Order right after placing it. Any other subject, and any subject of a
// missing Order, passes the view check before the result: a denied subject
// learns nothing about whether the Order exists.
func (h Handler) Handle(ctx context.Context, q Query) (orderdetails.View, error) {
	if err := q.Subject.Validate(); err != nil {
		return orderdetails.View{}, err
	}
	if q.OrderID == "" {
		return orderdetails.View{}, application.InvalidInput("order_id", "is required")
	}
	details, readErr := h.reader.OrderDetails(ctx, q.OrderID)
	missing := errors.Is(readErr, order.ErrOrderNotFound)
	if readErr != nil && !missing {
		return orderdetails.View{}, readErr
	}
	if missing || !q.Subject.IsUser(string(details.CustomerID)) {
		err := h.authorizer.Check(ctx, application.AccessCheck{
			Subject:    q.Subject,
			Permission: PermissionView,
			Resource:   application.Resource{Type: ResourceOrder, ID: string(q.OrderID)},
		})
		if err := application.FailClosed(err); err != nil {
			return orderdetails.View{}, err
		}
		if missing {
			return orderdetails.View{}, readErr
		}
	}
	return orderdetails.Present(ctx, h.policy, details), nil
}
