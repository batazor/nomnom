package infrastructure

import (
	"context"
	"errors"
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
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/cellrouter"
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
	market string
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

func (s *ordersServer) ListCustomerOrders(ctx context.Context, request *ordersv1.ListCustomerOrdersRequest) (*ordersv1.ListCustomerOrdersResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	s.market = request.GetMarketId()
	if s.err != nil {
		return nil, s.err
	}
	return &ordersv1.ListCustomerOrdersResponse{
		Orders: []*ordersv1.OrderSummary{
			{OrderId: "order-2", Cursor: "cursor-2", Order: card("order-2")},
			{OrderId: "order-1", Cursor: "cursor-1", Order: card("order-1")},
		},
		NextPageToken: request.GetCustomerId() + "/" + request.GetPageToken() + "/" + string(rune('0'+request.GetPageSize())),
	}, nil
}

// card is the full Order a history entry of orders carries.
func card(id string) *ordersv1.Order {
	return &ordersv1.Order{
		OrderId: id, RestaurantId: "restaurant-1", State: ordersv1.OrderState_ORDER_STATE_PLACED,
		Items: []*ordersv1.OrderItem{{
			MenuItemId: "menu-tea", Name: "Чай", Quantity: 1, UnitPrice: &ordersv1.Money{Amount: "0.10", Currency: "EUR"},
		}},
		Total:    &ordersv1.Money{Amount: "0.10", Currency: "EUR"},
		PlacedAt: timestamppb.New(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)),
	}
}

type restaurantsServer struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer
	seenMD metadata.MD
}

func (s *restaurantsServer) GetRestaurant(ctx context.Context, request *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{
		RestaurantId: request.GetRestaurantId(), Name: "Кухня №8", IsOpen: true,
	}}, nil
}

func credentials(t *testing.T) identity.OutgoingCredentials {
	t.Helper()
	dir := t.TempDir()
	for _, audience := range []string{"nomnom.orders", "nomnom.restaurants", "nomnom.delivery"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, audience), []byte("token-"+audience), 0o600))
	}
	signer, err := identity.NewAssertionSigner(filepath.Join(fixtures, "bff-signing-key.pem"), "nomnom-bff-test-key")
	require.NoError(t, err)
	return identity.OutgoingCredentials{ServiceTokens: identity.ServiceTokens{Dir: dir}, Signer: signer}
}

func connect(t *testing.T, audience string, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ids := credentials(t)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(identity.UnaryClientInterceptor(audience, ids.ServiceTokens, ids.Signer)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// customerContext is a request of customer-1 in Market berlin.
func customerContext() context.Context {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(cellrouter.MarketMetadata, "berlin"))
	return identity.WithUser(ctx, identity.User{ID: "customer-1"})
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
		ExpectedReadyAt: timestamppb.New(placedAt.Add(12 * time.Minute)),
	}}
	orders := NewOrders(connect(t, "nomnom.orders", func(s *grpc.Server) { ordersv1.RegisterOrdersServiceServer(s, server) }))

	got, err := orders.GetOrder(customerContext(), "order-1")

	require.NoError(t, err)
	assert.Equal(t, "confirmed", got.State)
	assert.Equal(t, []applications.Fact{{Kind: "preparation_started", OccurredAt: placedAt.Add(time.Minute)}}, got.Facts)
	assert.Equal(t, placedAt.Add(5*time.Minute), got.Window.ClosesAt)
	require.NotNil(t, got.Deadlines.ExpectedReadyAt)
	assert.Equal(t, placedAt.Add(12*time.Minute), *got.Deadlines.ExpectedReadyAt)
	assert.Nil(t, got.Deadlines.ExpectedPickupAt, "an unset deadline is not named")
	assert.Nil(t, got.Deadlines.ExpectedDeliveredAt, "an unset deadline is not named")
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

// AC-642: FAILED_PRECONDITION of GetOrder is an error of the read, not a
// refusal of cancellation.
func TestGetOrderFailedPreconditionIsNotACancellationRefusal(t *testing.T) {
	server := &ordersServer{err: refusal("ORDER_STATE_CONFLICT")}
	orders := NewOrders(connect(t, "nomnom.orders", func(s *grpc.Server) { ordersv1.RegisterOrdersServiceServer(s, server) }))
	_, err := orders.GetOrder(customerContext(), "order-1")
	require.Error(t, err)
	var refused applications.CancellationRefused
	assert.NotErrorAs(t, err, &refused)
	assert.NotErrorIs(t, err, applications.ErrOrderNotFound)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "the cause is kept for the log")
}

func TestCatalogIsPublic(t *testing.T) {
	server := &restaurantsServer{}
	catalog := NewCatalog(connect(t, "nomnom.restaurants", func(s *grpc.Server) { restaurantsv1.RegisterRestaurantsServiceServer(s, server) }))

	restaurant, err := catalog.Restaurant(customerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, restaurant)
	assert.Equal(t, []string{"Bearer token-nomnom.restaurants"}, server.seenMD.Get("authorization"))
	assert.Empty(t, server.seenMD.Get(identity.AssertionMetadata), "public read carries no user")
}

// The history goes to orders on behalf of the customer, with the customer id.
func TestOrdersListCustomerOrdersCarriesCustomerAndCursors(t *testing.T) {
	server := &ordersServer{}
	orders := NewOrders(connect(t, "nomnom.orders", func(s *grpc.Server) { ordersv1.RegisterOrdersServiceServer(s, server) }))

	page, err := orders.ListCustomerOrders(customerContext(), "customer-1", 2, "cursor-3")

	require.NoError(t, err)
	require.Len(t, page.Entries, 2)
	assert.Equal(t, "customer-1/cursor-3/2", page.NextCursor)
	assert.Equal(t, "berlin", server.market, "the history is of the request Market")
	assert.Equal(t, "cursor-2", page.Entries[0].Cursor)
	assert.Equal(t, "order-2", page.Entries[0].Order.ID)
	assert.Equal(t, []applications.OrderLine{{
		MenuItemID: "menu-tea", Name: "Чай", Quantity: 1, UnitPrice: applications.Money{Amount: "0.10", Currency: "EUR"},
	}}, page.Entries[0].Order.Items, "the card carries the item name kept by orders")
	assert.Len(t, server.seenMD.Get(identity.AssertionMetadata), 1)

	server.err = status.Error(codes.InvalidArgument, "cursor is malformed")
	_, err = orders.ListCustomerOrders(customerContext(), "customer-1", 2, "%%%")
	assert.ErrorIs(t, err, applications.ErrInvalidCursor)
	server.err = status.Error(codes.Unavailable, "down")
	_, err = orders.ListCustomerOrders(customerContext(), "customer-1", 2, "")
	assert.Equal(t, codes.Unavailable, status.Code(errors.Unwrap(err)))
}
