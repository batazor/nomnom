// Package neighbours provides interim client adapters of payments, restaurants
// and delivery for the Order Fulfillment Workflow until their command gRPC
// APIs exist.
package neighbours

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	ports "nomnom/orders/slices/fulfillment/applications/neighbours"
)

// ErrNotImplemented is retryable on purpose: the Activity keeps its step and
// continues once a worker with real clients is deployed.
var ErrNotImplemented = errors.New("command API of the neighbouring context is not implemented")

// Unavailable fails every call with ErrNotImplemented.
type Unavailable struct{}

var (
	_ ports.Payments    = Unavailable{}
	_ ports.Restaurants = Unavailable{}
	_ ports.Delivery    = Unavailable{}
)

func unavailable(api string) error { return fmt.Errorf("%s: %w", api, ErrNotImplemented) }

func (Unavailable) Authorize(context.Context, ports.PaymentRequest) (ports.PaymentDecision, error) {
	return ports.PaymentDecision{}, unavailable("payments")
}

func (Unavailable) Capture(context.Context, ports.PaymentRequest) (ports.PaymentDecision, error) {
	return ports.PaymentDecision{}, unavailable("payments")
}

func (Unavailable) CancelAuthorization(context.Context, ports.PaymentRequest) error {
	return unavailable("payments")
}

func (Unavailable) Refund(context.Context, ports.PaymentRequest) error {
	return unavailable("payments")
}

func (Unavailable) RequestOrder(context.Context, ports.RestaurantOrderRequest) error {
	return unavailable("restaurants")
}

func (Unavailable) CancelOrder(context.Context, ports.RestaurantOrderRequest) error {
	return unavailable("restaurants")
}

func (Unavailable) Create(context.Context, ports.DeliveryRequest) error {
	return unavailable("delivery")
}

func (Unavailable) Replan(context.Context, ports.DeliveryRequest) error {
	return unavailable("delivery")
}

func (Unavailable) Cancel(context.Context, ports.DeliveryRequest) error {
	return unavailable("delivery")
}

// Simulated approves every call without effect. It exists only to check the
// workflow route locally; facts of neighbours are sent to the workflow by hand.
type Simulated struct{}

var (
	_ ports.Payments    = Simulated{}
	_ ports.Restaurants = Simulated{}
	_ ports.Delivery    = Simulated{}
)

func simulated(call, key string) {
	slog.Info("simulated neighbour call", slog.String("call", call), slog.String("idempotency_key", key))
}

func (Simulated) Authorize(_ context.Context, r ports.PaymentRequest) (ports.PaymentDecision, error) {
	simulated("payments.Authorize", r.IdempotencyKey)
	return ports.PaymentDecision{Approved: true}, nil
}

func (Simulated) Capture(_ context.Context, r ports.PaymentRequest) (ports.PaymentDecision, error) {
	simulated("payments.Capture", r.IdempotencyKey)
	return ports.PaymentDecision{Approved: true}, nil
}

func (Simulated) CancelAuthorization(_ context.Context, r ports.PaymentRequest) error {
	simulated("payments.CancelAuthorization", r.IdempotencyKey)
	return nil
}

func (Simulated) Refund(_ context.Context, r ports.PaymentRequest) error {
	simulated("payments.Refund", r.IdempotencyKey)
	return nil
}

func (Simulated) RequestOrder(_ context.Context, r ports.RestaurantOrderRequest) error {
	simulated("restaurants.RequestOrder", r.IdempotencyKey)
	return nil
}

func (Simulated) CancelOrder(_ context.Context, r ports.RestaurantOrderRequest) error {
	simulated("restaurants.CancelOrder", r.IdempotencyKey)
	return nil
}

func (Simulated) Create(_ context.Context, r ports.DeliveryRequest) error {
	simulated("delivery.Create", r.IdempotencyKey)
	return nil
}

func (Simulated) Replan(_ context.Context, r ports.DeliveryRequest) error {
	simulated("delivery.Replan", r.IdempotencyKey)
	return nil
}

func (Simulated) Cancel(_ context.Context, r ports.DeliveryRequest) error {
	simulated("delivery.Cancel", r.IdempotencyKey)
	return nil
}
