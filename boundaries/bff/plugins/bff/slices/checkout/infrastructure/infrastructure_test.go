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

	"buf.build/gen/go/nomnom/orders/grpc/go/nomnom/orders/v1/ordersv1grpc"
	ordersv1 "buf.build/gen/go/nomnom/orders/protocolbuffers/go/nomnom/orders/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/checkout/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/identity"
)

const fixtures = "../../../../../../../tests/identity/fixtures"

type ordersServer struct {
	ordersv1grpc.UnimplementedOrdersServiceServer
	err    error
	got    *ordersv1.PlaceOrderRequest
	seenMD metadata.MD
}

func (s *ordersServer) PlaceOrder(ctx context.Context, request *ordersv1.PlaceOrderRequest) (*ordersv1.PlaceOrderResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	s.got = request
	if s.err != nil {
		return nil, s.err
	}
	return &ordersv1.PlaceOrderResponse{OrderId: "order-1"}, nil
}

func connect(t *testing.T, server *ordersServer) *Orders {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	ordersv1grpc.RegisterOrdersServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nomnom.orders"), []byte("token-nomnom.orders"), 0o600))
	signer, err := identity.NewAssertionSigner(filepath.Join(fixtures, "bff-signing-key.pem"), "nomnom-bff-test-key")
	require.NoError(t, err)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(identity.UnaryClientInterceptor("nomnom.orders", identity.ServiceTokens{Dir: dir}, signer)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return NewOrders(conn)
}

func customerContext() context.Context {
	return identity.WithUser(context.Background(), identity.User{ID: "customer-1"})
}

func refusal(code codes.Code, reason string, metadata map[string]string) error {
	st, _ := status.New(code, "refused").WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "nomnom.orders.v1", Metadata: metadata})
	return st.Err()
}

var input = applications.Input{
	RestaurantID:   "restaurant-1",
	Lines:          []applications.Line{{MenuItemID: "menu-pho", Quantity: 2}},
	Priority:       applications.PriorityStandard,
	IdempotencyKey: "attempt-1",
}

// FR-500: PlaceOrder goes to orders on behalf of the customer with the key.
func TestPlaceOrderCarriesCustomerAndIdempotencyKey(t *testing.T) {
	server := &ordersServer{}

	orderID, err := connect(t, server).PlaceOrder(customerContext(), input)

	require.NoError(t, err)
	assert.Equal(t, "order-1", orderID)
	assert.Equal(t, "attempt-1", server.got.GetIdempotencyKey())
	assert.Equal(t, "restaurant-1", server.got.GetRestaurantId())
	assert.Equal(t, "menu-pho", server.got.GetItems()[0].GetMenuItemId())
	assert.Equal(t, int32(2), server.got.GetItems()[0].GetQuantity())
	assert.Equal(t, ordersv1.OrderPriority_ORDER_PRIORITY_STANDARD, server.got.GetPriority())
	assert.Equal(t, []string{"Bearer token-nomnom.orders"}, server.seenMD.Get("authorization"))
	assert.Len(t, server.seenMD.Get(identity.AssertionMetadata), 1)
}

// AC-569: the metadata of ORDER_NOT_ORDERABLE reaches the scenario.
func TestPlaceOrderRefusalsCarryReasonAndMetadata(t *testing.T) {
	server := &ordersServer{err: refusal(codes.FailedPrecondition, "ORDER_NOT_ORDERABLE",
		map[string]string{"codes": "ORDER_ITEM_UNAVAILABLE", "menu_item_ids": "menu-gyoza"})}
	orders := connect(t, server)

	_, err := orders.PlaceOrder(customerContext(), input)
	assert.Equal(t, applications.PlacementRefused{
		Reason: "ORDER_NOT_ORDERABLE", Codes: []string{"ORDER_ITEM_UNAVAILABLE"}, MenuItemIDs: []string{"menu-gyoza"},
	}, err)

	server.err = refusal(codes.FailedPrecondition, "RESTAURANT_NOT_ACCEPTING_ORDERS", nil)
	_, err = orders.PlaceOrder(customerContext(), input)
	assert.Equal(t, applications.PlacementRefused{Reason: "RESTAURANT_NOT_ACCEPTING_ORDERS"}, err)
}

func TestPlaceOrderNamesOtherErrors(t *testing.T) {
	server := &ordersServer{err: refusal(codes.NotFound, "RESTAURANT_NOT_FOUND", nil)}
	orders := connect(t, server)

	_, err := orders.PlaceOrder(customerContext(), input)
	assert.ErrorIs(t, err, applications.ErrRestaurantNotFound)

	server.err = refusal(codes.InvalidArgument, "INVALID_ORDER", nil)
	_, err = orders.PlaceOrder(customerContext(), input)
	assert.ErrorIs(t, err, applications.ErrInvalidOrder)

	server.err = status.Error(codes.Unavailable, "down")
	_, err = orders.PlaceOrder(customerContext(), input)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, applications.ErrInvalidOrder)
	assert.NotErrorAs(t, err, new(applications.PlacementRefused))
}

// FR-650: a premium checkout reaches orders; PREMIUM_UNAVAILABLE is a refusal.
func TestPlaceOrderCarriesPremiumAndNamesItsRefusal(t *testing.T) {
	server := &ordersServer{}
	premium := input
	premium.Priority = applications.PriorityPremium
	_, err := connect(t, server).PlaceOrder(customerContext(), premium)
	require.NoError(t, err)
	assert.Equal(t, ordersv1.OrderPriority_ORDER_PRIORITY_PREMIUM, server.got.GetPriority())

	server = &ordersServer{err: refusal(codes.FailedPrecondition, "PREMIUM_UNAVAILABLE", nil)}
	_, err = connect(t, server).PlaceOrder(customerContext(), premium)
	assert.Equal(t, applications.PlacementRefused{Reason: "PREMIUM_UNAVAILABLE"}, err)
}

// FR-681: a scheduled checkout carries the start of the window, PREMIUM with it;
// SLOT_UNAVAILABLE carries the alternatives of its SlotUnavailable detail.
func TestPlaceOrderCarriesTheWindowAndReadsTheAlternatives(t *testing.T) {
	start := time.Date(2026, 10, 28, 17, 0, 0, 0, time.UTC)
	scheduled := input
	scheduled.Priority = applications.PriorityPremium
	scheduled.DeliveryWindowStart = &start
	server := &ordersServer{}
	_, err := connect(t, server).PlaceOrder(customerContext(), scheduled)
	require.NoError(t, err)
	assert.Equal(t, start, server.got.GetDeliveryWindowStart().AsTime())
	assert.Equal(t, ordersv1.OrderPriority_ORDER_PRIORITY_PREMIUM, server.got.GetPriority())

	asap := &ordersServer{}
	_, err = connect(t, asap).PlaceOrder(customerContext(), input)
	require.NoError(t, err)
	assert.Nil(t, asap.got.GetDeliveryWindowStart(), "an ASAP order has no window")

	st, _ := status.New(codes.FailedPrecondition, "slot unavailable").WithDetails(
		&errdetails.ErrorInfo{Reason: "SLOT_UNAVAILABLE", Domain: "nomnom.orders.v1"},
		&ordersv1.SlotUnavailable{Alternatives: []*ordersv1.DeliveryWindow{{
			Start: timestamppb.New(start.Add(15 * time.Minute)), End: timestamppb.New(start.Add(30 * time.Minute)),
		}}},
	)
	_, err = connect(t, &ordersServer{err: st.Err()}).PlaceOrder(customerContext(), scheduled)
	assert.Equal(t, applications.PlacementRefused{
		Reason:       "SLOT_UNAVAILABLE",
		Alternatives: []applications.Window{{Start: start.Add(15 * time.Minute), End: start.Add(30 * time.Minute)}},
	}, err)

	_, err = connect(t, &ordersServer{err: refusal(codes.FailedPrecondition, "SCHEDULED_ORDERS_UNAVAILABLE", nil)}).PlaceOrder(customerContext(), scheduled)
	assert.Equal(t, applications.PlacementRefused{Reason: "SCHEDULED_ORDERS_UNAVAILABLE"}, err)
}
