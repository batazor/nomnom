package grpcadapter_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	grpcadapter "nomnom/orders/infrastructure/grpc"
	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/gateway"
	restaurantsv1 "nomnom/orders/pkg/gateway/gen/nomnom/restaurants/v1"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/catalog"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

const identityFixtures = "../../../../tests/identity/fixtures"

// userVectors are the shared ADR 32 vectors: a BFF service token (delegate), a
// non-delegate service token and user assertions for customer-1.
type userVectors struct {
	Issuer           string            `json:"issuer"`
	Audience         string            `json:"audience"`
	Now              int64             `json:"now"`
	Delegates        string            `json:"delegates"`
	ServiceIssuer    string            `json:"service_issuer"`
	ServiceAccounts  string            `json:"service_accounts"`
	DelegateToken    string            `json:"delegate_token"`
	NonDelegateToken string            `json:"non_delegate_token"`
	ExpectedSubject  string            `json:"expected_subject"`
	Tokens           map[string]string `json:"tokens"`
}

func bufconnClient(t *testing.T, register func(*grpc.Server), options ...grpc.DialOption) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	options = append(options,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	conn, err := grpc.NewClient("passthrough:///bufnet", options...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// e2e is orders PlaceOrder with the production subject resolver (service token
// and BFF user assertion) and the production restaurants catalog adapter
// calling a mocked restaurants with the projected token of nomnom.restaurants.
type e2e struct {
	client     ordersv1.OrdersServiceClient
	vectors    userVectors
	store      *repositorytest.Store
	authorizer *MockAuthorizer
}

func newE2E(t *testing.T, restaurants *MockRestaurantsServiceServer) e2e {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(identityFixtures, "user-vectors.json"))
	require.NoError(t, err)
	var v userVectors
	require.NoError(t, json.Unmarshal(data, &v))
	resolver, err := grpcadapter.NewSubjectResolver(config.IdentityConfig{
		Issuer:          v.ServiceIssuer,
		Audience:        v.Audience,
		JWKSFile:        filepath.Join(identityFixtures, "jwks.json"),
		ServiceAccounts: v.ServiceAccounts,
		UserIssuer:      v.Issuer,
		UserJWKSFile:    filepath.Join(identityFixtures, "bff-jwks.json"),
		UserDelegates:   v.Delegates,
	})
	require.NoError(t, err)
	grpcadapter.PinClock(resolver, time.Unix(v.Now, 0))

	tokens := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tokens, "nomnom.restaurants"), []byte("orders-token\n"), 0o600))
	gatewayConn := bufconnClient(t, func(s *grpc.Server) {
		restaurantsv1.RegisterRestaurantsServiceServer(s, restaurants)
	},
		grpc.WithUnaryInterceptor(gateway.ServiceToken(tokens)))
	ports := commandPorts{
		workflow: NewMockCancellationRequester(t),
		clock:    fake.NewClock(ordertest.PlacedAt),
	}
	ports.catalog = catalog.New(gatewayConn,
		catalog.Limits{MinimumTotal: decimal.RequireFromString("0"), MaximumTotal: decimal.RequireFromString("100")}, time.Second)

	// Place Order checks no permission: the authorizer expects no call.
	store, authorizer := repositorytest.Open(t), NewMockAuthorizer(t)
	return e2e{
		client:     newClientWithPorts(t, resolver, store, authorizer, ports),
		vectors:    v,
		store:      store,
		authorizer: authorizer,
	}
}

// asBFF is a call of the BFF: its service token and, optionally, a user assertion.
func asBFF(token string, assertion string) context.Context {
	md := metadata.Pairs("authorization", "Bearer "+token)
	if assertion != "" {
		md.Append(grpcadapter.UserAssertionMetadata, assertion)
	}
	return metadata.NewOutgoingContext(context.Background(), md)
}

// menu is the restaurants menu of restaurant-1: pizza is available, gyoza is not.
var menu = &restaurantsv1.GetMenuResponse{Items: []*restaurantsv1.MenuItem{
	{MenuItemId: "pizza", Name: "Pizza", Price: &restaurantsv1.Money{Amount: "12.50", Currency: "EUR"}, Available: true},
	{MenuItemId: "gyoza", Name: "Gyoza", Price: &restaurantsv1.Money{Amount: "4", Currency: "EUR"}, Available: false},
}}

// AC-508, AC-525: the customer delegated by the BFF places an Order priced from
// the restaurants menu read through the gateway with the orders service token.
func TestPlaceOrderEndToEndForUserAssertedByBFF(t *testing.T) {
	restaurants := NewMockRestaurantsServiceServer(t)
	// restaurants sees the orders service token, not the BFF one.
	restaurants.EXPECT().GetRestaurant(mock.MatchedBy(func(ctx context.Context) bool {
		md, _ := metadata.FromIncomingContext(ctx)
		return assert.ObjectsAreEqual([]string{"Bearer orders-token"}, md.Get("authorization"))
	}), mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool { return r.GetRestaurantId() == "restaurant-1" })).
		Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: true, AcceptingOrders: true, MarketId: "berlin"}}, nil).Once()
	restaurants.EXPECT().GetMenu(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetMenuRequest) bool { return r.GetRestaurantId() == "restaurant-1" })).
		Return(menu, nil).Once()
	env := newE2E(t, restaurants)

	response, err := env.client.PlaceOrder(asBFF(env.vectors.DelegateToken, env.vectors.Tokens["valid"]), placeRequest())
	require.NoError(t, err)
	assert.Equal(t, "order-1", response.GetOrderId())

	snapshot, ok := env.store.Snapshot("order-1")
	require.True(t, ok)
	assert.Equal(t, order.CustomerID(env.vectors.ExpectedSubject), snapshot.CustomerID)
	assert.Equal(t, "berlin", snapshot.MarketID.String(), "the Order takes the Market of the Restaurant")
	require.Len(t, snapshot.Items, 1)
	assert.Equal(t, order.MenuItemID("pizza"), snapshot.Items[0].MenuItemID())
	assert.True(t, snapshot.Items[0].UnitPrice().Equal(ordertest.Money(t, "12.50")), "unit price comes from the restaurants menu")
	assert.Equal(t, []order.ID{"order-1"}, env.store.RelationshipChanges(), "orders-consumer syncs the relationships of the new Order")
}

func TestPlaceOrderEndToEndRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		restaurants func(*MockRestaurantsServiceServer)
		mutate      func(*ordersv1.PlaceOrderRequest)
		call        func(userVectors) context.Context
		code        codes.Code
		reason      string
	}{
		// AC-510: the BFF without a user assertion is a service, not a Customer.
		"bff without assertion": {func(*MockRestaurantsServiceServer) {}, nil,
			func(v userVectors) context.Context { return asBFF(v.DelegateToken, "") }, codes.PermissionDenied, "OPERATION_NOT_ALLOWED"},
		"restaurants unavailable": {func(r *MockRestaurantsServiceServer) {
			r.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool { return r.GetRestaurantId() == "restaurant-1" })).
				Return(nil, status.Error(codes.Unavailable, "down")).Once()
		}, nil, nil, codes.Unavailable, "DEPENDENCY_UNAVAILABLE"},
		"unknown restaurant": {func(r *MockRestaurantsServiceServer) {
			r.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool { return r.GetRestaurantId() == "restaurant-1" })).
				Return(nil, status.Error(codes.NotFound, "no restaurant")).Once()
		}, nil, nil, codes.NotFound, "RESTAURANT_NOT_FOUND"},
		"closed restaurant": {func(r *MockRestaurantsServiceServer) {
			r.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool { return r.GetRestaurantId() == "restaurant-1" })).
				Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: false, MarketId: "berlin"}}, nil).Once()
		}, nil, nil, codes.FailedPrecondition, "RESTAURANT_NOT_ACCEPTING_ORDERS"},
		"unknown menu item": {func(r *MockRestaurantsServiceServer) {
			r.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool { return r.GetRestaurantId() == "restaurant-1" })).
				Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: true, AcceptingOrders: true, MarketId: "berlin"}}, nil).Once()
			r.EXPECT().GetMenu(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetMenuRequest) bool { return r.GetRestaurantId() == "restaurant-1" })).
				Return(menu, nil).Once()
		}, func(r *ordersv1.PlaceOrderRequest) { r.Items[0].MenuItemId = "missing" }, nil, codes.FailedPrecondition, "ORDER_NOT_ORDERABLE"},
		"unavailable menu item": {func(r *MockRestaurantsServiceServer) {
			r.EXPECT().GetRestaurant(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetRestaurantRequest) bool { return r.GetRestaurantId() == "restaurant-1" })).
				Return(&restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: "restaurant-1", IsOpen: true, AcceptingOrders: true, MarketId: "berlin"}}, nil).Once()
			r.EXPECT().GetMenu(mock.Anything, mock.MatchedBy(func(r *restaurantsv1.GetMenuRequest) bool { return r.GetRestaurantId() == "restaurant-1" })).
				Return(menu, nil).Once()
		}, func(r *ordersv1.PlaceOrderRequest) { r.Items[0].MenuItemId = "gyoza" }, nil, codes.FailedPrecondition, "ORDER_NOT_ORDERABLE"},
	} {
		t.Run(name, func(t *testing.T) {
			restaurants := NewMockRestaurantsServiceServer(t)
			tc.restaurants(restaurants)
			env := newE2E(t, restaurants)
			request := placeRequest()
			if tc.mutate != nil {
				tc.mutate(request)
			}
			ctx := asBFF(env.vectors.DelegateToken, env.vectors.Tokens["valid"])
			if tc.call != nil {
				ctx = tc.call(env.vectors)
			}

			_, err := env.client.PlaceOrder(ctx, request)
			requireReason(t, err, tc.code, tc.reason)
			_, placed := env.store.Snapshot("order-1")
			assert.False(t, placed)
		})
	}
}

// AC-526: a user assertion from a service outside the delegates is UNAUTHENTICATED.
func TestPlaceOrderEndToEndRejectsAssertionFromNonDelegate(t *testing.T) {
	env := newE2E(t, NewMockRestaurantsServiceServer(t))

	_, err := env.client.PlaceOrder(asBFF(env.vectors.NonDelegateToken, env.vectors.Tokens["valid"]), placeRequest())
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, placed := env.store.Snapshot("order-1")
	assert.False(t, placed)
}
