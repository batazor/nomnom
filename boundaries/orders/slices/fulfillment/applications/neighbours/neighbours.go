// Package neighbours declares the client ports of payments, restaurants and
// delivery that the Activities of the Order Fulfillment Workflow call, their
// request DTOs and how a request is built from the Order snapshot. Adapters
// (infrastructure/neighbours, grpcclient) implement the ports; the Temporal
// adapter only uses them.
package neighbours

import (
	"context"

	"nomnom/orders/slices/order/domains/order"
)

// Orders reads the Order whose snapshot builds requests to neighbouring contexts.
type Orders interface {
	Get(ctx context.Context, id order.ID) (*order.Order, error)
}

// MarketID of PaymentRequest and DeliveryRequest is the Market of the Order
// (ADR 41). It is read from the stored Order on every Activity attempt, never
// carried in workflow input or state: workflows started before Markets get it
// the same way as new ones, and their Event History replays unchanged.
type PaymentRequest struct {
	OrderID        string
	CustomerID     string
	MarketID       string
	Amount         string
	Currency       string
	IdempotencyKey string
}

// PaymentDecision is the business result of a financial operation; a decline
// is a result, not an error.
type PaymentDecision struct {
	Approved bool
	Reason   string
}

// Payments is the client port of the business API of payments, called through
// the internal gateway under the worker service identity.
type Payments interface {
	Authorize(ctx context.Context, request PaymentRequest) (PaymentDecision, error)
	Capture(ctx context.Context, request PaymentRequest) (PaymentDecision, error)
	CancelAuthorization(ctx context.Context, request PaymentRequest) error
	Refund(ctx context.Context, request PaymentRequest) error
}

type RestaurantOrderItem struct {
	MenuItemID string
	Quantity   int
}

type RestaurantOrderRequest struct {
	OrderID      string
	RestaurantID string
	Items        []RestaurantOrderItem
	// Priority chosen at Place Order: the kitchen starts a premium order first
	// (FR-650).
	Priority       order.Priority
	IdempotencyKey string
}

// Restaurants is the client port of restaurants. Acceptance arrives later as
// RestaurantOrderAccepted or RestaurantOrderRejected.
type Restaurants interface {
	RequestOrder(ctx context.Context, request RestaurantOrderRequest) error
	CancelOrder(ctx context.Context, request RestaurantOrderRequest) error
}

type DeliveryRequest struct {
	OrderID        string
	MarketID       string
	IdempotencyKey string
}

// Delivery is the client port of delivery. Assignment, Pickup and Completion
// arrive later as Integration Events.
type Delivery interface {
	Create(ctx context.Context, request DeliveryRequest) error
	Replan(ctx context.Context, request DeliveryRequest) error
	Cancel(ctx context.Context, request DeliveryRequest) error
}

// NewPaymentRequest addresses the payment of the Order under the step key.
func NewPaymentRequest(current *order.Order, idempotencyKey string) PaymentRequest {
	return PaymentRequest{
		OrderID:        string(current.ID()),
		CustomerID:     string(current.CustomerID()),
		MarketID:       current.MarketID().String(),
		Amount:         current.Total().Amount().String(),
		Currency:       current.Total().Currency(),
		IdempotencyKey: idempotencyKey,
	}
}

// NewRestaurantOrderRequest carries the composition of the Order to restaurants.
func NewRestaurantOrderRequest(current *order.Order, idempotencyKey string) RestaurantOrderRequest {
	items := make([]RestaurantOrderItem, 0, len(current.Items()))
	for _, item := range current.Items() {
		items = append(items, RestaurantOrderItem{MenuItemID: string(item.MenuItemID()), Quantity: item.Quantity().Value()})
	}
	return RestaurantOrderRequest{
		OrderID:        string(current.ID()),
		RestaurantID:   string(current.RestaurantID()),
		Items:          items,
		Priority:       current.Service().Priority(),
		IdempotencyKey: idempotencyKey,
	}
}

// NewDeliveryRequest addresses the delivery of the Order under the step key.
func NewDeliveryRequest(current *order.Order, idempotencyKey string) DeliveryRequest {
	return DeliveryRequest{
		OrderID:        string(current.ID()),
		MarketID:       current.MarketID().String(),
		IdempotencyKey: idempotencyKey,
	}
}
