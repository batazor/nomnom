// Package orders declares what the Activities of the Order Fulfillment Workflow
// need from the Order, in the language of the process. The fulfillment slice does
// not import the order slice: the composition root (nomnom/orders/di) implements
// the port over the use cases of that slice and assigns usecase.ErrorKind to
// their errors.
package orders

import (
	"context"
	"time"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// Item is a position of the Order that restaurants prepares.
type Item struct {
	MenuItemID string
	Quantity   int
}

// DeliveryWindow is the Delivery Window of a Scheduled Order.
type DeliveryWindow struct {
	Start time.Time
	End   time.Time
}

// Order is the data of the stored Order that requests to neighbouring contexts
// are built from. DeliveryWindow is nil for an ASAP Order.
type Order struct {
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

// Plan is the schedule of a Scheduled Order.
type Plan struct {
	PlannedReadyAt   time.Time
	StartAt          time.Time
	CourierRequestAt time.Time
	ReplanAt         time.Time
}

// FinalDecision is the negative outcome chosen by the workflow after
// compensations. DecidedAt is the workflow time of the decision.
type FinalDecision struct {
	OrderID    string
	Outcome    fulfillment.Outcome
	ReasonCode string
	DecidedAt  time.Time
}

// Orders is the port of the Order. Moments are the workflow time, identical on
// every Activity attempt; a repeat after commit is a no-op.
type Orders interface {
	// Get reads the stored Order.
	Get(ctx context.Context, orderID string) (Order, error)
	RecordPaymentAuthorization(ctx context.Context, orderID string, authorizedAt time.Time) error
	Complete(ctx context.Context, orderID string, completedAt time.Time) error
	FailOrCancel(ctx context.Context, decision FinalDecision) error
	// PlanSchedule plans on fresh estimates; a replan passes the planned_ready_at
	// of the first plan, which does not move.
	PlanSchedule(ctx context.Context, orderID string, plannedReadyAt time.Time) (Plan, error)
}
