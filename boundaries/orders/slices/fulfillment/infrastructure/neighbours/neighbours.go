// Package neighbours provides interim client adapters of payments, restaurants
// and delivery for the Order Fulfillment Workflow until their command gRPC
// APIs exist.
package neighbours

import (
	"context"
	"errors"
	"fmt"
	"log"

	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
)

// ErrNotImplemented is retryable on purpose: the Activity keeps its step and
// continues once a worker with real clients is deployed.
var ErrNotImplemented = errors.New("command API of the neighbouring context is not implemented")

// Unavailable fails every call with ErrNotImplemented.
type Unavailable struct{}

var (
	_ temporal.Payments    = Unavailable{}
	_ temporal.Restaurants = Unavailable{}
	_ temporal.Delivery    = Unavailable{}
)

func unavailable(api string) error { return fmt.Errorf("%s: %w", api, ErrNotImplemented) }

func (Unavailable) Authorize(context.Context, temporal.PaymentRequest) (temporal.PaymentDecision, error) {
	return temporal.PaymentDecision{}, unavailable("payments")
}

func (Unavailable) Capture(context.Context, temporal.PaymentRequest) (temporal.PaymentDecision, error) {
	return temporal.PaymentDecision{}, unavailable("payments")
}

func (Unavailable) CancelAuthorization(context.Context, temporal.PaymentRequest) error {
	return unavailable("payments")
}

func (Unavailable) Refund(context.Context, temporal.PaymentRequest) error {
	return unavailable("payments")
}

func (Unavailable) RequestOrder(context.Context, temporal.RestaurantOrderRequest) error {
	return unavailable("restaurants")
}

func (Unavailable) CancelOrder(context.Context, temporal.RestaurantOrderRequest) error {
	return unavailable("restaurants")
}

func (Unavailable) Create(context.Context, temporal.DeliveryRequest) error {
	return unavailable("delivery")
}

func (Unavailable) Replan(context.Context, temporal.DeliveryRequest) error {
	return unavailable("delivery")
}

func (Unavailable) Cancel(context.Context, temporal.DeliveryRequest) error {
	return unavailable("delivery")
}

// Simulated approves every call without effect. It exists only to check the
// workflow route locally; facts of neighbours are sent to the workflow by hand.
type Simulated struct{}

var (
	_ temporal.Payments    = Simulated{}
	_ temporal.Restaurants = Simulated{}
	_ temporal.Delivery    = Simulated{}
)

func simulated(call, key string) {
	log.Printf("simulated neighbour call %s, idempotency key %s", call, key)
}

func (Simulated) Authorize(_ context.Context, r temporal.PaymentRequest) (temporal.PaymentDecision, error) {
	simulated("payments.Authorize", r.IdempotencyKey)
	return temporal.PaymentDecision{Approved: true}, nil
}

func (Simulated) Capture(_ context.Context, r temporal.PaymentRequest) (temporal.PaymentDecision, error) {
	simulated("payments.Capture", r.IdempotencyKey)
	return temporal.PaymentDecision{Approved: true}, nil
}

func (Simulated) CancelAuthorization(_ context.Context, r temporal.PaymentRequest) error {
	simulated("payments.CancelAuthorization", r.IdempotencyKey)
	return nil
}

func (Simulated) Refund(_ context.Context, r temporal.PaymentRequest) error {
	simulated("payments.Refund", r.IdempotencyKey)
	return nil
}

func (Simulated) RequestOrder(_ context.Context, r temporal.RestaurantOrderRequest) error {
	simulated("restaurants.RequestOrder", r.IdempotencyKey)
	return nil
}

func (Simulated) CancelOrder(_ context.Context, r temporal.RestaurantOrderRequest) error {
	simulated("restaurants.CancelOrder", r.IdempotencyKey)
	return nil
}

func (Simulated) Create(_ context.Context, r temporal.DeliveryRequest) error {
	simulated("delivery.Create", r.IdempotencyKey)
	return nil
}

func (Simulated) Replan(_ context.Context, r temporal.DeliveryRequest) error {
	simulated("delivery.Replan", r.IdempotencyKey)
	return nil
}

func (Simulated) Cancel(_ context.Context, r temporal.DeliveryRequest) error {
	simulated("delivery.Cancel", r.IdempotencyKey)
	return nil
}
