package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	deliveryv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/delivery/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// Couriers reads the courier of an Order from delivery.v1.DeliveryService
// (GetOrderCourier) on behalf of the customer: delivery checks order#view.
type Couriers struct {
	client deliveryv1.DeliveryServiceClient
}

func NewCouriers(conn grpc.ClientConnInterface) *Couriers {
	return &Couriers{client: deliveryv1.NewDeliveryServiceClient(conn)}
}

var _ applications.Couriers = (*Couriers)(nil)

// CourierOf keeps the customer of ctx; an empty answer is an Order without a
// courier.
func (c *Couriers) CourierOf(ctx context.Context, orderID string) (*applications.Courier, error) {
	response, err := c.client.GetOrderCourier(ctx, &deliveryv1.GetOrderCourierRequest{OrderId: orderID})
	if err != nil {
		return nil, fmt.Errorf("get order courier: %w", err)
	}
	if response.GetCourierId() == "" {
		return nil, nil
	}
	return &applications.Courier{Name: response.GetName()}, nil
}
