package estimates_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/planschedule"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/estimates"

	"buf.build/gen/go/nomnom/delivery/grpc/go/nomnom/delivery/v1/deliveryv1grpc"
	deliveryv1 "buf.build/gen/go/nomnom/delivery/protocolbuffers/go/nomnom/delivery/v1"
	"buf.build/gen/go/nomnom/restaurants/grpc/go/nomnom/restaurants/v1/restaurantsv1grpc"
	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"
)

func serve(t *testing.T) (*grpc.ClientConn, *MockRestaurantsServiceServer, *MockDeliveryServiceServer) {
	t.Helper()
	restaurants := NewMockRestaurantsServiceServer(t)
	delivery := NewMockDeliveryServiceServer(t)
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	restaurantsv1grpc.RegisterRestaurantsServiceServer(server, restaurants)
	deliveryv1grpc.RegisterDeliveryServiceServer(server, delivery)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///gateway",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, restaurants, delivery
}

func TestBaselinePreparationOfTheRestaurant(t *testing.T) {
	conn, restaurants, _ := serve(t)
	restaurants.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool {
		return proto.Equal(r, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
	})).Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{
		RestaurantId: "restaurant-1", BaselinePreparation: durationpb.New(25 * time.Minute),
	}}, nil).Once()

	baseline, err := estimates.NewRestaurants(conn).BaselinePreparation(context.Background(), "restaurant-1")

	require.NoError(t, err)
	assert.Equal(t, 25*time.Minute, baseline)
}

func TestBaselinePreparationMissingIsAContractViolation(t *testing.T) {
	conn, restaurants, _ := serve(t)
	restaurants.EXPECT().GetRestaurant(mock.Anything, mock.Anything).
		Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1"}}, nil).Once()

	_, err := estimates.NewRestaurants(conn).BaselinePreparation(context.Background(), "restaurant-1")

	require.ErrorIs(t, err, estimates.ErrMissingEstimate)
}

func TestDeliveryDurationOfTheMarket(t *testing.T) {
	conn, _, delivery := serve(t)
	delivery.EXPECT().EstimateDeliveryDuration(mock.Anything, mock.MatchedBy(func(r *deliveryv1.EstimateDeliveryDurationRequest) bool {
		return proto.Equal(r, &deliveryv1.EstimateDeliveryDurationRequest{MarketId: ordertest.Market})
	})).Return(&deliveryv1.EstimateDeliveryDurationResponse{
		CourierLead: durationpb.New(15 * time.Minute), Transit: durationpb.New(20 * time.Minute),
	}, nil).Once()

	estimate, err := estimates.NewDelivery(conn).EstimateDeliveryDuration(context.Background(), ordertest.MarketID(t))

	require.NoError(t, err)
	assert.Equal(t, planschedule.DeliveryEstimate{CourierLead: 15 * time.Minute, Transit: 20 * time.Minute}, estimate)
}

func TestEstimateErrorsKeepTransportRetryable(t *testing.T) {
	for code, want := range map[codes.Code]error{
		codes.Unavailable:     usecase.ErrDependencyUnavailable,
		codes.InvalidArgument: usecase.ErrInvalidInput,
	} {
		t.Run(code.String(), func(t *testing.T) {
			conn, _, delivery := serve(t)
			delivery.EXPECT().EstimateDeliveryDuration(mock.Anything, mock.Anything).Return(nil, status.Error(code, "boom")).Once()

			_, err := estimates.NewDelivery(conn).EstimateDeliveryDuration(context.Background(), ordertest.MarketID(t))

			require.ErrorIs(t, err, want)
		})
	}
}
