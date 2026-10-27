package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	deliveryv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/delivery/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// orderDeliveries answers GetOrderDelivery and records whether the call
// carried the customer as a user assertion.
type orderDeliveries struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	response *deliveryv1.GetOrderDeliveryResponse
	err      error
	asserted bool
}

func (s *orderDeliveries) GetOrderDelivery(ctx context.Context, _ *deliveryv1.GetOrderDeliveryRequest) (*deliveryv1.GetOrderDeliveryResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.asserted = len(md.Get(identity.AssertionMetadata)) == 1
	return s.response, s.err
}

func deliveriesOver(t *testing.T, server *orderDeliveries) *Deliveries {
	t.Helper()
	return NewDeliveries(connect(t, "nomnom.delivery", func(s *grpc.Server) {
		deliveryv1.RegisterDeliveryServiceServer(s, server)
	}))
}

func point(latitude, longitude float64) *deliveryv1.GeoPoint {
	return &deliveryv1.GeoPoint{Latitude: latitude, Longitude: longitude}
}

// AC-795, AC-884: the courier and the tracking of the Order come in one call
// on behalf of the customer.
func TestDeliveryOfTheOrderIsReadForTheCustomer(t *testing.T) {
	server := &orderDeliveries{response: &deliveryv1.GetOrderDeliveryResponse{
		Courier: &deliveryv1.OrderCourier{CourierId: "courier-1", Name: "Тимур"},
		Tracking: &deliveryv1.OrderTracking{
			TrackingChannel: "cd",
			City:            &deliveryv1.VirtualCity{Origin: point(52.515, 13.385), Blocks: 12, BlockMeters: 150},
			Pickup:          point(52.52, 13.39),
			Dropoff:         point(52.525, 13.40),
			CourierPosition: &deliveryv1.CourierLocation{Latitude: 52.521, Longitude: 13.392, RecordedAtEpochSeconds: 1_793_100_000},
		},
	}}

	delivery, err := deliveriesOver(t, server).DeliveryOf(customerContext(), "order-1")

	require.NoError(t, err)
	assert.True(t, server.asserted)
	assert.Equal(t, applications.OrderDelivery{
		Courier: &applications.Courier{Name: "Тимур"},
		Tracking: &applications.Tracking{
			Channel: "cd",
			City: applications.VirtualCity{
				Origin:      applications.GeoPoint{Latitude: 52.515, Longitude: 13.385},
				Blocks:      12,
				BlockMeters: 150,
			},
			Pickup:  applications.GeoPoint{Latitude: 52.52, Longitude: 13.39},
			Dropoff: applications.GeoPoint{Latitude: 52.525, Longitude: 13.40},
			CourierPosition: &applications.CourierPosition{
				Channel:    "cd",
				Point:      applications.GeoPoint{Latitude: 52.521, Longitude: 13.392},
				RecordedAt: time.Unix(1_793_100_000, 0).UTC(),
			},
		},
	}, delivery)
}

// AC-886: unset parts are an Order without a courier or tracking; tracking
// without the city is a broken contract; a failure is an error.
func TestDeliveryOfTheOrderWithoutParts(t *testing.T) {
	server := &orderDeliveries{response: &deliveryv1.GetOrderDeliveryResponse{}}
	delivery, err := deliveriesOver(t, server).DeliveryOf(customerContext(), "order-1")
	require.NoError(t, err)
	assert.Equal(t, applications.OrderDelivery{}, delivery)

	server.response = &deliveryv1.GetOrderDeliveryResponse{Tracking: &deliveryv1.OrderTracking{TrackingChannel: "cd"}}
	_, err = deliveriesOver(t, server).DeliveryOf(customerContext(), "order-1")
	assert.Error(t, err, "a channel without the city is a broken contract")

	server.err = status.Error(codes.Unavailable, "down")
	_, err = deliveriesOver(t, server).DeliveryOf(customerContext(), "order-1")
	assert.Error(t, err)
}
