// Package applications holds the order tracking scenario of the customer UI:
// one Order of the current customer and its cancellation. It composes data of
// orders and restaurants into the customer view; Protobuf and GraphQL types do
// not reach it (BFF ADR 2).
package applications

import (
	"errors"
	"time"
)

// ErrOrderNotFound covers a missing Order and an Order of another customer:
// the customer must not learn which one it is.
var ErrOrderNotFound = errors.New("order not found")

// Money is an exact amount: a decimal string with a dot separator.
type Money struct {
	Amount   string
	Currency string
}

// OrderSnapshot is the Order as orders publishes it to an authorized customer.
type OrderSnapshot struct {
	ID           string
	RestaurantID string
	State        string
	Items        []OrderLine
	Total        Money
	PlacedAt     time.Time
	Facts        []Fact
	// Cancellation is set only for a cancelled Order.
	Cancellation *Cancellation
	// Window is set only while the Order State admits cancellation.
	Window *CancellationWindow
	// Deadlines named by the kitchen and the courier; nil until named.
	Deadlines Deadlines
}

// Deadlines are the latest fulfillment deadlines of an Order (FR-611): the
// kitchen names ExpectedReadyAt, the courier ExpectedPickupAt and
// ExpectedDeliveredAt. A deadline nobody named is nil.
type Deadlines struct {
	ExpectedReadyAt     *time.Time
	ExpectedPickupAt    *time.Time
	ExpectedDeliveredAt *time.Time
}

type OrderLine struct {
	MenuItemID string
	// Name is the menu item name kept at Place Order; empty for Orders placed
	// before orders kept it.
	Name      string
	Quantity  int
	UnitPrice Money
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

// Restaurant is the public restaurant data of restaurants.
type Restaurant struct {
	ID     string
	Name   string
	IsOpen bool
}
