// Package listcustomerorders returns the order history of one Customer.
package listcustomerorders

import (
	"context"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/domains/order"
)

const (
	PermissionViewOrders application.Permission   = "view_orders"
	ResourceCustomer     application.ResourceType = "customer"

	DefaultLimit = 20
	MaxLimit     = 100
)

type Query struct {
	Subject    application.Subject
	CustomerID order.CustomerID
	// Limit defaults to DefaultLimit and must not exceed MaxLimit.
	Limit int
	// Cursor is the opaque NextCursor of the previous page.
	Cursor string
}

// Entry is one Order of the history: its full card, so a reader of the
// history needs no Get Order per Order.
type Entry struct {
	orderdetails.View
	// Cursor continues the history after this Order, like Page.NextCursor.
	Cursor string
}

// Page lists orders from newest to oldest; NextCursor is empty on the last page.
type Page struct {
	Orders     []Entry
	NextCursor string
}

// Row is one row of the OrderDetails projection on a history page.
type Row struct {
	Details orderdetails.Details
	Cursor  string
}

// Rows is a history page as the Reader returns it.
type Rows struct {
	Rows       []Row
	NextCursor string
}

type PageRequest struct {
	CustomerID order.CustomerID
	Limit      int
	Cursor     string
}

type Authorizer interface {
	Check(ctx context.Context, check application.AccessCheck) error
}

// Reader pages the OrderDetails projection filtered by CustomerID and returns
// application.ErrInvalidInput for a malformed cursor.
type Reader interface {
	ListCustomerOrders(ctx context.Context, request PageRequest) (Rows, error)
}

type Handler struct {
	authorizer Authorizer
	reader     Reader
	policy     orderdetails.CancellationPolicy
}

func NewHandler(authorizer Authorizer, reader Reader, policy orderdetails.CancellationPolicy) Handler {
	return Handler{authorizer: authorizer, reader: reader, policy: policy}
}

func (h Handler) Handle(ctx context.Context, q Query) (Page, error) {
	if err := q.Subject.Validate(); err != nil {
		return Page{}, err
	}
	if q.CustomerID == "" {
		return Page{}, application.InvalidInput("customer_id", "is required")
	}
	limit := q.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0 || limit > MaxLimit:
		return Page{}, application.InvalidInput("limit", "must be between 1 and 100")
	}

	// The owner is known to domain storage, so the Customer's own history is
	// filtered by customer_id without the authorization graph.
	owner := q.Subject.Kind == application.SubjectUser && q.Subject.ID == string(q.CustomerID)
	if !owner {
		err := h.authorizer.Check(ctx, application.AccessCheck{
			Subject:    q.Subject,
			Permission: PermissionViewOrders,
			Resource:   application.Resource{Type: ResourceCustomer, ID: string(q.CustomerID)},
		})
		if err := application.FailClosed(err); err != nil {
			return Page{}, err
		}
	}
	rows, err := h.reader.ListCustomerOrders(ctx, PageRequest{CustomerID: q.CustomerID, Limit: limit, Cursor: q.Cursor})
	if err != nil {
		return Page{}, err
	}
	page := Page{Orders: make([]Entry, 0, len(rows.Rows)), NextCursor: rows.NextCursor}
	for _, row := range rows.Rows {
		page.Orders = append(page.Orders, Entry{View: orderdetails.Present(ctx, h.policy, row.Details), Cursor: row.Cursor})
	}
	return page, nil
}
