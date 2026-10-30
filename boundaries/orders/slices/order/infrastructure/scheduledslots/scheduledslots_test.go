package scheduledslots_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	restaurantsv1 "nomnom/orders/infrastructure/gateway/gen/nomnom/restaurants/v1"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/scheduledslots"
)

var windowStart = time.Date(2026, time.October, 25, 19, 0, 0, 0, time.UTC)

func newSlots(t *testing.T, server *MockRestaurantsServiceServer) *scheduledslots.Slots {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	restaurantsv1.RegisterRestaurantsServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return scheduledslots.New(conn, time.Second)
}

func reserveRequest() *restaurantsv1.ReserveScheduledSlotRequest {
	return &restaurantsv1.ReserveScheduledSlotRequest{
		RestaurantId: "restaurant-1",
		WindowStart:  timestamppb.New(windowStart),
		OrderId:      "order-1",
	}
}

func window(start time.Time) *restaurantsv1.DeliveryWindow {
	return &restaurantsv1.DeliveryWindow{Start: timestamppb.New(start), End: timestamppb.New(start.Add(15 * time.Minute))}
}

func TestReserveReturnsTheReservedWindow(t *testing.T) {
	server := NewMockRestaurantsServiceServer(t)
	server.EXPECT().ReserveScheduledSlot(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.ReserveScheduledSlotRequest) bool {
		return proto.Equal(r, reserveRequest())
	})).Return(&restaurantsv1.ReserveScheduledSlotResponse{
		Result: &restaurantsv1.ReserveScheduledSlotResponse_Reserved{Reserved: window(windowStart)},
	}, nil).Once()

	reserved, err := newSlots(t, server).Reserve(context.Background(), "restaurant-1", windowStart, "order-1")

	require.NoError(t, err)
	assert.Equal(t, windowStart, reserved.Start())
	assert.Equal(t, windowStart.Add(15*time.Minute), reserved.End())
}

func TestReserveFullWindowIsSlotUnavailableWithAlternatives(t *testing.T) {
	server := NewMockRestaurantsServiceServer(t)
	server.EXPECT().ReserveScheduledSlot(mock.Anything, mock.Anything).Return(&restaurantsv1.ReserveScheduledSlotResponse{
		Result: &restaurantsv1.ReserveScheduledSlotResponse_SlotUnavailable{SlotUnavailable: &restaurantsv1.SlotUnavailable{
			Alternatives: []*restaurantsv1.DeliveryWindow{window(windowStart.Add(15 * time.Minute))},
		}},
	}, nil).Once()

	_, err := newSlots(t, server).Reserve(context.Background(), "restaurant-1", windowStart, "order-1")

	var unavailable placeorder.SlotUnavailable
	require.ErrorAs(t, err, &unavailable)
	require.Len(t, unavailable.Alternatives, 1)
	assert.Equal(t, windowStart.Add(15*time.Minute), unavailable.Alternatives[0].Start())
}

func TestReserveMapsErrors(t *testing.T) {
	scheduledUnavailable, err := status.New(codes.FailedPrecondition, "no settings").WithDetails(&errdetails.ErrorInfo{
		Reason: scheduledslots.ReasonScheduledOrdersUnavailable, Domain: "nomnom.restaurants.v1",
	})
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		err  error
		want error
	}{
		"window off the grid":          {err: status.Error(codes.InvalidArgument, "off the grid"), want: usecase.ErrInvalidInput},
		"unknown restaurant":           {err: status.Error(codes.NotFound, "no restaurant"), want: placeorder.ErrUnknownRestaurant},
		"scheduled orders unavailable": {err: scheduledUnavailable.Err(), want: placeorder.ErrScheduledOrdersUnavailable},
		"unknown precondition":         {err: status.Error(codes.FailedPrecondition, "other"), want: scheduledslots.ErrInvalidReservation},
		"transport":                    {err: status.Error(codes.Unavailable, "down"), want: usecase.ErrDependencyUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			server := NewMockRestaurantsServiceServer(t)
			server.EXPECT().ReserveScheduledSlot(mock.Anything, mock.Anything).Return(nil, tc.err).Once()

			_, err := newSlots(t, server).Reserve(context.Background(), "restaurant-1", windowStart, order.ID("order-1"))

			require.ErrorIs(t, err, tc.want)
		})
	}
}
