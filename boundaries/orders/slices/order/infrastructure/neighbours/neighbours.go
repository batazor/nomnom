// Package neighbours provides interim client adapters of payments, restaurants
// and delivery for the Order Fulfillment Workflow until their command gRPC
// APIs exist.
package neighbours

import (
	"context"
	"errors"
	"fmt"
	"log"

	"nomnom/orders/slices/order/infrastructure/temporal/fulfillorder"
)

// ErrNotImplemented is retryable on purpose: the Activity keeps its step and
// continues once a worker with real clients is deployed.
var ErrNotImplemented = errors.New("command API of the neighbouring context is not implemented")

// Unavailable fails every call with ErrNotImplemented.
type Unavailable struct{}

var (
	_ fulfillorder.Payments    = Unavailable{}
	_ fulfillorder.Restaurants = Unavailable{}
	_ fulfillorder.Delivery    = Unavailable{}
)

func unavailable(api string) error { return fmt.Errorf("%s: %w", api, ErrNotImplemented) }

func (Unavailable) Authorize(context.Context, fulfillorder.PaymentRequest) (fulfillorder.PaymentDecision, error) {
	return fulfillorder.PaymentDecision{}, unavailable("payments")
}

func (Unavailable) Capture(context.Context, fulfillorder.PaymentRequest) (fulfillorder.PaymentDecision, error) {
	return fulfillorder.PaymentDecision{}, unavailable("payments")
}

func (Unavailable) CancelAuthorization(context.Context, fulfillorder.PaymentRequest) error {
	return unavailable("payments")
}

func (Unavailable) Refund(context.Context, fulfillorder.PaymentRequest) error {
	return unavailable("payments")
}

func (Unavailable) RequestOrder(context.Context, fulfillorder.RestaurantOrderRequest) error {
	return unavailable("restaurants")
}

func (Unavailable) CancelOrder(context.Context, fulfillorder.RestaurantOrderRequest) error {
	return unavailable("restaurants")
}

func (Unavailable) Create(context.Context, fulfillorder.DeliveryRequest) error {
	return unavailable("delivery")
}

func (Unavailable) Replan(context.Context, fulfillorder.DeliveryRequest) error {
	return unavailable("delivery")
}

func (Unavailable) Cancel(context.Context, fulfillorder.DeliveryRequest) error {
	return unavailable("delivery")
}

// Simulated approves every call without effect. It exists only to check the
// workflow route locally; facts of neighbours are sent to the workflow by hand.
type Simulated struct{}

var (
	_ fulfillorder.Payments    = Simulated{}
	_ fulfillorder.Restaurants = Simulated{}
	_ fulfillorder.Delivery    = Simulated{}
)

func simulated(call, key string) {
	log.Printf("simulated neighbour call %s, idempotency key %s", call, key)
}

func (Simulated) Authorize(_ context.Context, r fulfillorder.PaymentRequest) (fulfillorder.PaymentDecision, error) {
	simulated("payments.Authorize", r.IdempotencyKey)
	return fulfillorder.PaymentDecision{Approved: true}, nil
}

func (Simulated) Capture(_ context.Context, r fulfillorder.PaymentRequest) (fulfillorder.PaymentDecision, error) {
	simulated("payments.Capture", r.IdempotencyKey)
	return fulfillorder.PaymentDecision{Approved: true}, nil
}

func (Simulated) CancelAuthorization(_ context.Context, r fulfillorder.PaymentRequest) error {
	simulated("payments.CancelAuthorization", r.IdempotencyKey)
	return nil
}

func (Simulated) Refund(_ context.Context, r fulfillorder.PaymentRequest) error {
	simulated("payments.Refund", r.IdempotencyKey)
	return nil
}

func (Simulated) RequestOrder(_ context.Context, r fulfillorder.RestaurantOrderRequest) error {
	simulated("restaurants.RequestOrder", r.IdempotencyKey)
	return nil
}

func (Simulated) CancelOrder(_ context.Context, r fulfillorder.RestaurantOrderRequest) error {
	simulated("restaurants.CancelOrder", r.IdempotencyKey)
	return nil
}

func (Simulated) Create(_ context.Context, r fulfillorder.DeliveryRequest) error {
	simulated("delivery.Create", r.IdempotencyKey)
	return nil
}

func (Simulated) Replan(_ context.Context, r fulfillorder.DeliveryRequest) error {
	simulated("delivery.Replan", r.IdempotencyKey)
	return nil
}

func (Simulated) Cancel(_ context.Context, r fulfillorder.DeliveryRequest) error {
	simulated("delivery.Cancel", r.IdempotencyKey)
	return nil
}
