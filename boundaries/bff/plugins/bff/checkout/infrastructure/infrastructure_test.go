package infrastructure

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	ordersv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/orders/v1"
)

const fixtures = "../../../../../../tests/identity/fixtures"

type ordersServer struct {
	ordersv1.UnimplementedOrdersServiceServer
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
	return &ordersv1.PlaceOrderResponse{OrderId: "order-1", FulfillmentStarted: true}, nil
}

func connect(t *testing.T, server *ordersServer) *Orders {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	ordersv1.RegisterOrdersServiceServer(grpcServer, server)
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
	RestaurantID:    "restaurant-1",
	Lines:           []applications.Line{{MenuItemID: "menu-pho", Quantity: 2}},
	DeliveryAddress: "ул. Садовая, 8",
	IdempotencyKey:  "attempt-1",
}

// FR-500: PlaceOrder goes to orders on behalf of the customer with the key.
func TestPlaceOrderCarriesCustomerAndIdempotencyKey(t *testing.T) {
	server := &ordersServer{}

	orderID, err := connect(t, server).PlaceOrder(customerContext(), input)

	require.NoError(t, err)
	assert.Equal(t, "order-1", orderID)
	assert.Equal(t, "attempt-1", server.got.GetIdempotencyKey())
	assert.Equal(t, "restaurant-1", server.got.GetRestaurantId())
	assert.Equal(t, "ул. Садовая, 8", server.got.GetDeliveryAddress())
	assert.Equal(t, "menu-pho", server.got.GetItems()[0].GetMenuItemId())
	assert.Equal(t, int32(2), server.got.GetItems()[0].GetQuantity())
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
