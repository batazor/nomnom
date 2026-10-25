package di

import (
	"context"

	ports "nomnom/orders/slices/fulfillment/applications/neighbours"
	fulfillmentdi "nomnom/orders/slices/fulfillment/di"
)

// approvingNeighbours approves every call of payments, restaurants and
// delivery without effect, so a workflow runs its route in tests.
type approvingNeighbours struct{}

func (approvingNeighbours) Authorize(context.Context, ports.PaymentRequest) (ports.PaymentDecision, error) {
	return ports.PaymentDecision{Approved: true}, nil
}

func (approvingNeighbours) Capture(context.Context, ports.PaymentRequest) (ports.PaymentDecision, error) {
	return ports.PaymentDecision{Approved: true}, nil
}

func (approvingNeighbours) CancelAuthorization(context.Context, ports.PaymentRequest) error {
	return nil
}

func (approvingNeighbours) Refund(context.Context, ports.PaymentRequest) error { return nil }

func (approvingNeighbours) RequestOrder(context.Context, ports.RestaurantOrderRequest) error {
	return nil
}

func (approvingNeighbours) CancelOrder(context.Context, ports.RestaurantOrderRequest) error {
	return nil
}

func (approvingNeighbours) Create(context.Context, ports.DeliveryRequest) error { return nil }

func (approvingNeighbours) Replan(context.Context, ports.DeliveryRequest) error { return nil }

func (approvingNeighbours) Cancel(context.Context, ports.DeliveryRequest) error { return nil }

func testNeighbours() fulfillmentdi.Neighbours {
	return fulfillmentdi.Neighbours{Payments: approvingNeighbours{}, Restaurants: approvingNeighbours{}, Delivery: approvingNeighbours{}}
}
