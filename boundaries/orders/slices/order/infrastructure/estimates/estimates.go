// Package estimates implements the estimate ports of Plan Schedule on the
// internal gateway: the baseline preparation time from GetRestaurant of
// restaurants and EstimateDeliveryDuration of delivery, called under the
// service identity of the orders worker (FR-677).
package estimates

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deliveryv1 "nomnom/orders/infrastructure/gateway/gen/nomnom/delivery/v1"
	restaurantsv1 "nomnom/orders/infrastructure/gateway/gen/nomnom/restaurants/v1"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/planschedule"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// ErrMissingEstimate means a neighbour answered without the estimate it
// always publishes: a broken contract, never a duration chosen here.
var ErrMissingEstimate = errors.New("estimate missing from the response")

// Restaurants reads Restaurant.baseline_preparation.
type Restaurants struct {
	client restaurantsv1.RestaurantsServiceClient
}

var _ planschedule.Preparation = (*Restaurants)(nil)

func NewRestaurants(conn grpc.ClientConnInterface) *Restaurants {
	return &Restaurants{client: restaurantsv1.NewRestaurantsServiceClient(conn)}
}

func (r *Restaurants) BaselinePreparation(ctx context.Context, restaurantID order.RestaurantID) (time.Duration, error) {
	resp, err := r.client.GetRestaurant(ctx, &restaurantsv1.GetRestaurantRequest{RestaurantId: string(restaurantID)})
	if err != nil {
		return 0, mapError("restaurants.GetRestaurant", err)
	}
	baseline := resp.GetRestaurant().GetBaselinePreparation()
	if baseline == nil {
		return 0, fmt.Errorf("restaurants.GetRestaurant %q: baseline_preparation: %w", restaurantID, ErrMissingEstimate)
	}
	if err := baseline.CheckValid(); err != nil {
		return 0, fmt.Errorf("restaurants.GetRestaurant %q: baseline_preparation: %w", restaurantID, err)
	}
	return baseline.AsDuration(), nil
}

// Delivery reads EstimateDeliveryDuration of a Market.
type Delivery struct {
	client deliveryv1.DeliveryServiceClient
}

var _ planschedule.DeliveryDuration = (*Delivery)(nil)

func NewDelivery(conn grpc.ClientConnInterface) *Delivery {
	return &Delivery{client: deliveryv1.NewDeliveryServiceClient(conn)}
}

func (d *Delivery) EstimateDeliveryDuration(ctx context.Context, marketID vo.MarketID) (planschedule.DeliveryEstimate, error) {
	resp, err := d.client.EstimateDeliveryDuration(ctx, &deliveryv1.EstimateDeliveryDurationRequest{MarketId: marketID.String()})
	if err != nil {
		return planschedule.DeliveryEstimate{}, mapError("delivery.EstimateDeliveryDuration", err)
	}
	if resp.GetCourierLead() == nil || resp.GetTransit() == nil {
		return planschedule.DeliveryEstimate{}, fmt.Errorf("delivery.EstimateDeliveryDuration %q: %w", marketID, ErrMissingEstimate)
	}
	for _, d := range []interface{ CheckValid() error }{resp.GetCourierLead(), resp.GetTransit()} {
		if err := d.CheckValid(); err != nil {
			return planschedule.DeliveryEstimate{}, fmt.Errorf("delivery.EstimateDeliveryDuration %q: %w", marketID, err)
		}
	}
	return planschedule.DeliveryEstimate{
		CourierLead: resp.GetCourierLead().AsDuration(),
		Transit:     resp.GetTransit().AsDuration(),
	}, nil
}

// mapError makes contract violations non-retryable through
// usecase.ErrInvalidInput, like the command clients of the worker;
// transport and identity failures stay retryable until fixed by deployment.
func mapError(call string, err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.NotFound, codes.FailedPrecondition:
		return fmt.Errorf("%s: %w: %w", call, usecase.ErrInvalidInput, err)
	default:
		return fmt.Errorf("%s: %w: %w", call, usecase.ErrDependencyUnavailable, err)
	}
}
