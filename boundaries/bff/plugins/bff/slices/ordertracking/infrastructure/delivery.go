package infrastructure

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	"buf.build/gen/go/nomnom/delivery/grpc/go/nomnom/delivery/v1/deliveryv1grpc"
	deliveryv1 "buf.build/gen/go/nomnom/delivery/protocolbuffers/go/nomnom/delivery/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/ordertracking/applications"
)

// Deliveries reads the delivery of an Order — its courier and live
// tracking — from delivery.v1.DeliveryService (GetOrderDelivery) on behalf of
// the customer: delivery checks order#view once.
type Deliveries struct {
	client deliveryv1grpc.DeliveryServiceClient
}

func NewDeliveries(conn grpc.ClientConnInterface) *Deliveries {
	return &Deliveries{client: deliveryv1grpc.NewDeliveryServiceClient(conn)}
}

var _ applications.Deliveries = (*Deliveries)(nil)

// DeliveryOf keeps the customer of ctx; an unset part is an Order without a
// courier or without tracking. Tracking with a channel but without the city
// or points is a broken contract, not an Order without tracking.
func (d *Deliveries) DeliveryOf(ctx context.Context, orderID string) (applications.OrderDelivery, error) {
	response, err := d.client.GetOrderDelivery(ctx, &deliveryv1.GetOrderDeliveryRequest{OrderId: orderID})
	if err != nil {
		return applications.OrderDelivery{}, fmt.Errorf("get order delivery: %w", err)
	}
	var delivery applications.OrderDelivery
	if courier := response.GetCourier(); courier != nil {
		delivery.Courier = &applications.Courier{Name: courier.GetName()}
	}
	if tracking := response.GetTracking(); tracking != nil {
		delivery.Tracking, err = trackingOf(orderID, tracking)
		if err != nil {
			return applications.OrderDelivery{}, err
		}
	}
	return delivery, nil
}

func trackingOf(orderID string, response *deliveryv1.OrderTracking) (*applications.Tracking, error) {
	channel := response.GetTrackingChannel()
	city := response.GetCity()
	if channel == "" || city == nil || city.GetOrigin() == nil || response.GetPickup() == nil || response.GetDropoff() == nil {
		return nil, fmt.Errorf("get order delivery: incomplete tracking of order %s", orderID)
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
