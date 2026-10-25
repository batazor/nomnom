package infrastructure

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	ordersv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/orders/v1"
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

const fixtures = "../../../../../../tests/identity/fixtures"

var placedAt = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

type ordersServer struct {
	ordersv1.UnimplementedOrdersServiceServer
	order  *ordersv1.Order
	err    error
	seenMD metadata.MD
}

func (s *ordersServer) GetOrder(ctx context.Context, _ *ordersv1.GetOrderRequest) (*ordersv1.GetOrderResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	if s.err != nil {
		return nil, s.err
	}
	return &ordersv1.GetOrderResponse{Order: s.order}, nil
}

func (s *ordersServer) RequestOrderCancellation(ctx context.Context, _ *ordersv1.RequestOrderCancellationRequest) (*ordersv1.RequestOrderCancellationResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	if s.err != nil {
		return nil, s.err
	}
	return &ordersv1.RequestOrderCancellationResponse{Outcome: ordersv1.CancellationRequestOutcome_CANCELLATION_REQUEST_OUTCOME_REQUESTED}, nil
}

type restaurantsServer struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer
	menuErr error
	seenMD  metadata.MD
}

func (s *restaurantsServer) GetRestaurant(ctx context.Context, request *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{
		RestaurantId: request.GetRestaurantId(), Name: "Кухня №8", Address: "ул. Садовая, 8", IsOpen: true,
	}}, nil
}

func (s *restaurantsServer) GetMenu(context.Context, *restaurantsv1.GetMenuRequest) (*restaurantsv1.GetMenuResponse, error) {
	if s.menuErr != nil {
		return nil, s.menuErr
	}
	return &restaurantsv1.GetMenuResponse{Items: []*restaurantsv1.MenuItem{{MenuItemId: "menu-tea", Name: "Чай"}}}, nil
}

func customers(t *testing.T) *identity.Identity {
	t.Helper()
	dir := t.TempDir()
	for _, audience := range []string{"nomnom.orders", "nomnom.restaurants"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, audience), []byte("token-"+audience), 0o600))
	}
	signer, err := identity.NewAssertionSigner(filepath.Join(fixtures, "bff-signing-key.pem"), "nomnom-bff-test-key")
	require.NoError(t, err)
	return &identity.Identity{Assertions: signer, ServiceTokens: identity.ServiceTokens{Dir: dir}}
}

func connect(t *testing.T, audience string, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ids := customers(t)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(identity.UnaryClientInterceptor(audience, ids.ServiceTokens, ids.Assertions)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func customerContext() context.Context {
	return identity.WithUser(context.Background(), identity.User{ID: "customer-1"})
}

func refusal(reason string) error {
	st, _ := status.New(codes.FailedPrecondition, "refused").WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "nomnom.orders.v1"})
	return st.Err()
}

// FR-490: GetOrder goes to orders on behalf of the customer.
func TestOrdersGetOrderCarriesCustomerAndMapsOrder(t *testing.T) {
	server := &ordersServer{order: &ordersv1.Order{
		OrderId: "order-1", RestaurantId: "restaurant-1", State: ordersv1.OrderState_ORDER_STATE_CONFIRMED,
		Items:            []*ordersv1.OrderItem{{MenuItemId: "menu-tea", Quantity: 3, UnitPrice: &ordersv1.Money{Amount: "0.10", Currency: "EUR"}}},
		Total:            &ordersv1.Money{Amount: "0.30", Currency: "EUR"},
		PlacedAt:         timestamppb.New(placedAt),
		FulfillmentFacts: []*ordersv1.FulfillmentFact{{Kind: "preparation_started", OccurredAt: timestamppb.New(placedAt.Add(time.Minute))}},
		CancellationWindow: &ordersv1.CancellationWindow{
			OpensAt: timestamppb.New(placedAt), ClosesAt: timestamppb.New(placedAt.Add(5 * time.Minute)),
		},
	}}
	orders := NewOrders(connect(t, "nomnom.orders", func(s *grpc.Server) { ordersv1.RegisterOrdersServiceServer(s, server) }))

	got, err := orders.GetOrder(customerContext(), "order-1")

	require.NoError(t, err)
	assert.Equal(t, "confirmed", got.State)
	assert.Equal(t, []applications.Fact{{Kind: "preparation_started", OccurredAt: placedAt.Add(time.Minute)}}, got.Facts)
	assert.Equal(t, placedAt.Add(5*time.Minute), got.Window.ClosesAt)
	assert.Equal(t, []string{"Bearer token-nomnom.orders"}, server.seenMD.Get("authorization"))
	assert.Len(t, server.seenMD.Get(identity.AssertionMetadata), 1)
}

// AC-555: a foreign or missing Order is the same not found.
func TestOrdersHidesForeignOrders(t *testing.T) {
	for _, code := range []codes.Code{codes.NotFound, codes.PermissionDenied} {
		server := &ordersServer{err: status.Error(code, "no")}
		orders := NewOrders(connect(t, "nomnom.orders", func(s *grpc.Server) { ordersv1.RegisterOrdersServiceServer(s, server) }))
		_, err := orders.GetOrder(customerContext(), "order-1")
		assert.ErrorIs(t, err, applications.ErrOrderNotFound, code)
	}
}

// AC-560: the ErrorInfo reason of a refusal reaches the scenario.
func TestOrdersCancellationRefusalCarriesReason(t *testing.T) {
	server := &ordersServer{err: refusal("CANCELLATION_WINDOW_EXPIRED")}
	orders := NewOrders(connect(t, "nomnom.orders", func(s *grpc.Server) { ordersv1.RegisterOrdersServiceServer(s, server) }))

	_, err := orders.RequestCancellation(customerContext(), applications.CancellationRequest{OrderID: "order-1", RequestID: "r-1"})

	assert.Equal(t, applications.CancellationRefused{Reason: "CANCELLATION_WINDOW_EXPIRED"}, err)

	server.err = nil
	outcome, err := orders.RequestCancellation(customerContext(), applications.CancellationRequest{OrderID: "order-1", RequestID: "r-1"})
	require.NoError(t, err)
	assert.Equal(t, applications.OutcomeRequested, outcome)
}

func TestOrdersUnavailableStaysAnError(t *testing.T) {
	server := &ordersServer{err: status.Error(codes.Unavailable, "down")}
	orders := NewOrders(connect(t, "nomnom.orders", func(s *grpc.Server) { ordersv1.RegisterOrdersServiceServer(s, server) }))
	_, err := orders.GetOrder(customerContext(), "order-1")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, applications.ErrOrderNotFound)
}

func TestCatalogIsPublicAndToleratesMissingMenu(t *testing.T) {
	server := &restaurantsServer{}
	catalog := NewCatalog(connect(t, "nomnom.restaurants", func(s *grpc.Server) { restaurantsv1.RegisterRestaurantsServiceServer(s, server) }))

	restaurant, err := catalog.Restaurant(customerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", Address: "ул. Садовая, 8", IsOpen: true}, restaurant)
	assert.Equal(t, []string{"Bearer token-nomnom.restaurants"}, server.seenMD.Get("authorization"))
	assert.Empty(t, server.seenMD.Get(identity.AssertionMetadata), "public read carries no user")

	names, err := catalog.MenuItemNames(customerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"menu-tea": "Чай"}, names)

	server.menuErr = status.Error(codes.NotFound, "no menu")
	names, err = catalog.MenuItemNames(customerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Empty(t, names)
}
