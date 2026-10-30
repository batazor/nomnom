// Package infrastructure adapts the Simulator port to the GetSimulator and
// SetSimulatorEnabled RPCs of orders, restaurants and delivery, called through
// the internal gateway with the service token of BFF and the user assertion
// of the verified user (ADR 32). Each context authorizes
// platform#operate_simulator itself.
package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"buf.build/gen/go/nomnom/delivery/grpc/go/nomnom/delivery/v1/deliveryv1grpc"
	deliveryv1 "buf.build/gen/go/nomnom/delivery/protocolbuffers/go/nomnom/delivery/v1"
	"buf.build/gen/go/nomnom/orders/grpc/go/nomnom/orders/v1/ordersv1grpc"
	ordersv1 "buf.build/gen/go/nomnom/orders/protocolbuffers/go/nomnom/orders/v1"
	"buf.build/gen/go/nomnom/restaurants/grpc/go/nomnom/restaurants/v1/restaurantsv1grpc"
	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/simulator/applications"
)

// Orders implements applications.Simulator for orders.
type Orders struct {
	client ordersv1grpc.OrdersServiceClient
}

func NewOrders(conn grpc.ClientConnInterface) *Orders {
	return &Orders{client: ordersv1grpc.NewOrdersServiceClient(conn)}
}

func (o *Orders) Enabled(ctx context.Context) (bool, error) {
	response, err := o.client.GetSimulator(ctx, &ordersv1.GetSimulatorRequest{})
	if err != nil {
		return false, simulatorError("get orders simulator", err)
	}
	return response.GetEnabled(), nil
}

func (o *Orders) SetEnabled(ctx context.Context, enabled bool) (bool, error) {
	response, err := o.client.SetSimulatorEnabled(ctx, &ordersv1.SetSimulatorEnabledRequest{Enabled: enabled})
	if err != nil {
		return false, simulatorError("set orders simulator", err)
	}
	return response.GetEnabled(), nil
}

// Restaurants implements applications.Simulator for restaurants.
type Restaurants struct {
	client restaurantsv1grpc.RestaurantsServiceClient
}

func NewRestaurants(conn grpc.ClientConnInterface) *Restaurants {
	return &Restaurants{client: restaurantsv1grpc.NewRestaurantsServiceClient(conn)}
}

func (r *Restaurants) Enabled(ctx context.Context) (bool, error) {
	response, err := r.client.GetSimulator(ctx, &restaurantsv1.GetSimulatorRequest{})
	if err != nil {
		return false, simulatorError("get restaurants simulator", err)
	}
	return response.GetEnabled(), nil
}

func (r *Restaurants) SetEnabled(ctx context.Context, enabled bool) (bool, error) {
	response, err := r.client.SetSimulatorEnabled(ctx, &restaurantsv1.SetSimulatorEnabledRequest{Enabled: enabled})
	if err != nil {
		return false, simulatorError("set restaurants simulator", err)
	}
	return response.GetEnabled(), nil
}

// Delivery implements applications.Simulator for delivery.
type Delivery struct {
	client deliveryv1grpc.DeliveryServiceClient
}

func NewDelivery(conn grpc.ClientConnInterface) *Delivery {
	return &Delivery{client: deliveryv1grpc.NewDeliveryServiceClient(conn)}
}

func (d *Delivery) Enabled(ctx context.Context) (bool, error) {
	response, err := d.client.GetSimulator(ctx, &deliveryv1.GetSimulatorRequest{})
	if err != nil {
		return false, simulatorError("get delivery simulator", err)
	}
	return response.GetEnabled(), nil
}

func (d *Delivery) SetEnabled(ctx context.Context, enabled bool) (bool, error) {
	response, err := d.client.SetSimulatorEnabled(ctx, &deliveryv1.SetSimulatorEnabledRequest{Enabled: enabled})
	if err != nil {
		return false, simulatorError("set delivery simulator", err)
	}
	return response.GetEnabled(), nil
}

var (
	_ applications.Simulator = (*Orders)(nil)
	_ applications.Simulator = (*Restaurants)(nil)
	_ applications.Simulator = (*Delivery)(nil)
)

// simulatorError names the refusal of a context; UNAVAILABLE and the rest
// stay infrastructure failures.
func simulatorError(operation string, err error) error {
	if status.Code(err) == codes.PermissionDenied {
		return applications.ErrNotPermitted
	}
	return fmt.Errorf("%s: %w", operation, err)
}
