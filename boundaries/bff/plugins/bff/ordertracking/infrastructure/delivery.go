package infrastructure

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	deliveryv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/delivery/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// Couriers reads the courier of an Order and its live tracking from
// delivery.v1.DeliveryService (GetOrderCourier, GetOrderTracking) on behalf
// of the customer: delivery checks order#view.
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

// TrackingOf keeps the customer of ctx; an empty answer is an Order without
// tracking. An answer with a channel but without the city or points is a
// broken contract, not an Order without tracking.
func (c *Couriers) TrackingOf(ctx context.Context, orderID string) (*applications.Tracking, error) {
	response, err := c.client.GetOrderTracking(ctx, &deliveryv1.GetOrderTrackingRequest{OrderId: orderID})
	if err != nil {
		return nil, fmt.Errorf("get order tracking: %w", err)
	}
	channel := response.GetTrackingChannel()
	if channel == "" {
		return nil, nil
	}
	city := response.GetCity()
	if city == nil || city.GetOrigin() == nil || response.GetPickup() == nil || response.GetDropoff() == nil {
		return nil, fmt.Errorf("get order tracking: incomplete tracking of order %s", orderID)
	}
	tracking := &applications.Tracking{
		Channel: channel,
		City: applications.VirtualCity{
			Origin:      geoPoint(city.GetOrigin()),
			Blocks:      int(city.GetBlocks()),
			BlockMeters: city.GetBlockMeters(),
		},
		Pickup:  geoPoint(response.GetPickup()),
		Dropoff: geoPoint(response.GetDropoff()),
	}
	if position := response.GetCourierPosition(); position != nil {
		tracking.CourierPosition = &applications.CourierPosition{
			Channel:    channel,
			Point:      applications.GeoPoint{Latitude: position.GetLatitude(), Longitude: position.GetLongitude()},
			RecordedAt: time.Unix(position.GetRecordedAtEpochSeconds(), 0).UTC(),
		}
	}
	return tracking, nil
}

func geoPoint(point *deliveryv1.GeoPoint) applications.GeoPoint {
	return applications.GeoPoint{Latitude: point.GetLatitude(), Longitude: point.GetLongitude()}
}
