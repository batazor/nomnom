// Package applications holds the order tracking scenario of the customer UI:
// one Order of the current customer and its cancellation. It composes data of
// orders and restaurants into the customer view; Protobuf and GraphQL types do
// not reach it (BFF ADR 2).
package applications

import (
	"context"
	"errors"
	"time"
)

// ErrOrderNotFound covers a missing Order and an Order of another customer:
// the customer must not learn which one it is.
var ErrOrderNotFound = errors.New("order not found")

// CancellationRefused is a business refusal of orders to accept a
// Cancellation Request. Reason is its ErrorInfo reason, for example
// CANCELLATION_WINDOW_EXPIRED or ORDER_STATE_CONFLICT.
type CancellationRefused struct {
	Reason string
}

func (e CancellationRefused) Error() string { return "cancellation refused: " + e.Reason }

// Money is an exact amount: a decimal string with a dot separator.
type Money struct {
	Amount   string
	Currency string
}

// OrderSnapshot is the Order as orders publishes it to an authorized customer.
type OrderSnapshot struct {
	ID              string
	RestaurantID    string
	State           string
	Items           []OrderLine
	Total           Money
	DeliveryAddress string
	PlacedAt        time.Time
	Facts           []Fact
	// Cancellation is set only for a cancelled Order.
	Cancellation *Cancellation
	// Window is set only while the Order State admits cancellation.
	Window *CancellationWindow
}

type OrderLine struct {
	MenuItemID string
	Quantity   int
	UnitPrice  Money
}

// Fact is a Fulfillment Fact reflected in the Order, for example picked_up.
type Fact struct {
	Kind       string
	OccurredAt time.Time
}

type Cancellation struct {
	Outcome     string
	ReasonCode  string
	CancelledAt time.Time
}

type CancellationWindow struct {
	OpensAt  time.Time
	ClosesAt time.Time
}

// Orders is the orders API on behalf of the customer in the request context.
type Orders interface {
	GetOrder(ctx context.Context, orderID string) (OrderSnapshot, error)
	RequestCancellation(ctx context.Context, request CancellationRequest) (CancellationOutcome, error)
}

type CancellationRequest struct {
	OrderID   string
	RequestID string
	Reason    string
}

// CancellationOutcome is the accepted result of a Cancellation Request.
type CancellationOutcome string

const (
	OutcomeRequested        CancellationOutcome = "REQUESTED"
	OutcomeAlreadyCancelled CancellationOutcome = "ALREADY_CANCELLED"
)

// Restaurant is the public restaurant data of restaurants.
type Restaurant struct {
	ID      string
	Name    string
	Address string
	IsOpen  bool
}

// Catalog reads public data of restaurants: no customer is involved.
type Catalog interface {
	Restaurant(ctx context.Context, restaurantID string) (Restaurant, error)
	// MenuItemNames returns the current names of menu items by id.
	MenuItemNames(ctx context.Context, restaurantID string) (map[string]string, error)
}
