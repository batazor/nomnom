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

// orderCouriers answers GetOrderCourier and records whether the call carried
// the customer as a user assertion.
type orderCouriers struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	response *deliveryv1.GetOrderCourierResponse
	tracking *deliveryv1.GetOrderTrackingResponse
	err      error
	asserted bool
}

func (s *orderCouriers) GetOrderTracking(ctx context.Context, _ *deliveryv1.GetOrderTrackingRequest) (*deliveryv1.GetOrderTrackingResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.asserted = len(md.Get(identity.AssertionMetadata)) == 1
	return s.tracking, s.err
}

func (s *orderCouriers) GetOrderCourier(ctx context.Context, _ *deliveryv1.GetOrderCourierRequest) (*deliveryv1.GetOrderCourierResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.asserted = len(md.Get(identity.AssertionMetadata)) == 1
	return s.response, s.err
}

func couriersOver(t *testing.T, server *orderCouriers) *Couriers {
	t.Helper()
	return NewCouriers(connect(t, "nomnom.delivery", func(s *grpc.Server) {
		deliveryv1.RegisterDeliveryServiceServer(s, server)
	}))
}

// AC-795: the courier of the Order is read on behalf of the customer.
func TestCourierOfTheOrderIsReadForTheCustomer(t *testing.T) {
	server := &orderCouriers{response: &deliveryv1.GetOrderCourierResponse{CourierId: "courier-1", Name: "Тимур"}}

	courier, err := couriersOver(t, server).CourierOf(customerContext(), "order-1")

	require.NoError(t, err)
	assert.Equal(t, &applications.Courier{Name: "Тимур"}, courier)
	assert.True(t, server.asserted)

	server.response = &deliveryv1.GetOrderCourierResponse{}
	courier, err = couriersOver(t, server).CourierOf(customerContext(), "order-1")
	require.NoError(t, err)
	assert.Nil(t, courier, "an empty answer is no courier")

	server.err = status.Error(codes.Unavailable, "down")
	_, err = couriersOver(t, server).CourierOf(customerContext(), "order-1")
	assert.Error(t, err)
}

// AC-884, AC-886: the tracking is read on behalf of the customer; an empty
// answer is no tracking, an incomplete one is an error.
func TestTrackingOfTheOrderIsReadForTheCustomer(t *testing.T) {
	point := func(latitude, longitude float64) *deliveryv1.GeoPoint {
		return &deliveryv1.GeoPoint{Latitude: latitude, Longitude: longitude}
	}
	server := &orderCouriers{tracking: &deliveryv1.GetOrderTrackingResponse{
		TrackingChannel: "cd",
		City:            &deliveryv1.VirtualCity{Origin: point(52.515, 13.385), Blocks: 12, BlockMeters: 150},
		Pickup:          point(52.52, 13.39),
		Dropoff:         point(52.525, 13.40),
		CourierPosition: &deliveryv1.CourierLocation{Latitude: 52.521, Longitude: 13.392, RecordedAtEpochSeconds: 1_793_100_000},
	}}

	tracking, err := couriersOver(t, server).TrackingOf(customerContext(), "order-1")

	require.NoError(t, err)
	assert.True(t, server.asserted)
	assert.Equal(t, &applications.Tracking{
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
	}, tracking)

	server.tracking = &deliveryv1.GetOrderTrackingResponse{}
	tracking, err = couriersOver(t, server).TrackingOf(customerContext(), "order-1")
	require.NoError(t, err)
	assert.Nil(t, tracking, "an empty answer is no tracking")

	server.tracking = &deliveryv1.GetOrderTrackingResponse{TrackingChannel: "cd"}
	_, err = couriersOver(t, server).TrackingOf(customerContext(), "order-1")
	assert.Error(t, err, "a channel without the city is a broken contract")
}
