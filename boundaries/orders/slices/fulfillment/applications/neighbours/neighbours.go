// Package neighbours declares the client ports of payments, restaurants and
// delivery that the Activities of the Order Fulfillment Workflow call, their
// request DTOs and how a request is built from the view of the Order. Adapters
// (infrastructure/neighbours, grpcclient) implement the ports; the Temporal
// adapter only uses them.
package neighbours

import (
	"context"
	"time"

	"nomnom/orders/slices/order/applications/getfulfillmentorder"
)

// Priority is the Premium Priority of the Order the kitchen starts it by.
type Priority string

const (
	PriorityStandard Priority = "STANDARD"
	PriorityPremium  Priority = "PREMIUM"
)

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
	Priority       Priority
	IdempotencyKey string
	// Schedule is set for a Scheduled Order (FR-683): the kitchen accepts it
	// into ACCEPTED and the workflow queues it at its planned start.
	Schedule *RestaurantOrderSchedule
}

// RestaurantOrderSchedule is the Delivery Window of the Order and the
// planned_ready_at of the first plan.
type RestaurantOrderSchedule struct {
	WindowStart    time.Time
	WindowEnd      time.Time
	PlannedReadyAt time.Time
}

// SlotRequest addresses the slot reservation of the Order: restaurants keys it
// by order_id.
type SlotRequest struct {
	OrderID        string
	IdempotencyKey string
}

// SlotDecision is the business result of ConfirmScheduledSlot: a window that
// filled up after its hold expired is not confirmed — a result, not an error.
type SlotDecision struct {
	Confirmed bool
}

// QueueRequest moves the accepted Restaurant Order of a Scheduled Order to the
// Kitchen Queue (FR-224).
type QueueRequest struct {
	OrderID           string
	RestaurantOrderID string
	IdempotencyKey    string
}

// Restaurants is the client port of restaurants. Acceptance arrives later as
// RestaurantOrderAccepted or RestaurantOrderRejected.
type Restaurants interface {
	// RequestOrder returns the restaurant_order_id; a repeat returns the same.
	RequestOrder(ctx context.Context, request RestaurantOrderRequest) (string, error)
	CancelOrder(ctx context.Context, request RestaurantOrderRequest) error
	QueueOrder(ctx context.Context, request QueueRequest) error
	ConfirmSlot(ctx context.Context, request SlotRequest) (SlotDecision, error)
	// ReleaseSlot succeeds for a missing reservation.
	ReleaseSlot(ctx context.Context, request SlotRequest) error
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
	Cancel(ctx context.Context, request DeliveryRequest) error
}

// NewPaymentRequest addresses the payment of the Order under the step key.
func NewPaymentRequest(current getfulfillmentorder.View, idempotencyKey string) PaymentRequest {
	return PaymentRequest{
		OrderID:        current.OrderID,
		CustomerID:     current.CustomerID,
		MarketID:       current.MarketID,
		Amount:         current.Amount,
		Currency:       current.Currency,
		IdempotencyKey: idempotencyKey,
	}
}

// NewRestaurantOrderRequest carries the composition of the Order to restaurants.
func NewRestaurantOrderRequest(current getfulfillmentorder.View, idempotencyKey string) RestaurantOrderRequest {
	items := make([]RestaurantOrderItem, 0, len(current.Items))
	for _, item := range current.Items {
		items = append(items, RestaurantOrderItem{MenuItemID: item.MenuItemID, Quantity: item.Quantity})
	}
	return RestaurantOrderRequest{
		OrderID:        current.OrderID,
		RestaurantID:   current.RestaurantID,
		Items:          items,
		Priority:       Priority(current.Priority),
		IdempotencyKey: idempotencyKey,
	}
}

// NewSlotRequest addresses the slot reservation of the Order under the step key.
func NewSlotRequest(current getfulfillmentorder.View, idempotencyKey string) SlotRequest {
	return SlotRequest{OrderID: current.OrderID, IdempotencyKey: idempotencyKey}
}

// NewDeliveryRequest addresses the delivery of the Order under the step key.
func NewDeliveryRequest(current getfulfillmentorder.View, idempotencyKey string) DeliveryRequest {
	return DeliveryRequest{
		OrderID:        current.OrderID,
		MarketID:       current.MarketID,
		IdempotencyKey: idempotencyKey,
	}
}
