package grpcadapter_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
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

// fakeMenu serves GetRestaurant and GetMenu of restaurants behind the internal gateway.
type fakeMenu struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer

	mu            sync.Mutex
	open          bool
	items         []*restaurantsv1.MenuItem
	err           error
	authorization []string
}

func (f *fakeMenu) GetRestaurant(ctx context.Context, r *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	md, _ := metadata.FromIncomingContext(ctx)
	f.authorization = md.Get("authorization")
	if f.err != nil {
		return nil, f.err
	}
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: r.GetRestaurantId(), IsOpen: f.open, MarketId: "berlin"}}, nil
}

func (f *fakeMenu) GetMenu(context.Context, *restaurantsv1.GetMenuRequest) (*restaurantsv1.GetMenuResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &restaurantsv1.GetMenuResponse{Items: f.items}, nil
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
// calling a fake restaurants with the projected token of nomnom.restaurants.
type e2e struct {
	client     ordersv1.OrdersServiceClient
	vectors    userVectors
	store      *repositorytest.Store
	authorizer *fake.Authorizer
}

func newE2E(t *testing.T, restaurants *fakeMenu) e2e {
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
	gatewayConn := bufconnClient(t, func(s *grpc.Server) { restaurantsv1.RegisterRestaurantsServiceServer(s, restaurants) },
		grpc.WithUnaryInterceptor(gateway.ServiceToken(tokens)))
	ports := newCommandPorts()
	ports.catalog = catalog.New(gatewayConn,
		catalog.Limits{MinimumTotal: decimal.RequireFromString("0"), MaximumTotal: decimal.RequireFromString("100")}, time.Second)

	store, authorizer := repositorytest.Open(t), fake.NewAuthorizer()
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

func openMenu() *fakeMenu {
	return &fakeMenu{open: true, items: []*restaurantsv1.MenuItem{
		{MenuItemId: "pizza", Name: "Pizza", Price: &restaurantsv1.Money{Amount: "12.50", Currency: "EUR"}, Available: true},
		{MenuItemId: "gyoza", Name: "Gyoza", Price: &restaurantsv1.Money{Amount: "4", Currency: "EUR"}, Available: false},
	}}
}

// AC-508, AC-525: the customer delegated by the BFF places an Order priced from
// the restaurants menu read through the gateway with the orders service token.
func TestPlaceOrderEndToEndForUserAssertedByBFF(t *testing.T) {
	restaurants := openMenu()
	env := newE2E(t, restaurants)

	response, err := env.client.PlaceOrder(asBFF(env.vectors.DelegateToken, env.vectors.Tokens["valid"]), placeRequest())
	require.NoError(t, err)
	assert.Equal(t, "order-1", response.GetOrderId())
	assert.True(t, response.GetFulfillmentStarted())

	snapshot, ok := env.store.Snapshot("order-1")
	require.True(t, ok)
	assert.Equal(t, order.CustomerID(env.vectors.ExpectedSubject), snapshot.CustomerID)
	assert.Equal(t, "berlin", snapshot.MarketID.String(), "the Order takes the Market of the Restaurant")
	require.Len(t, snapshot.Items, 1)
	assert.Equal(t, order.MenuItemID("pizza"), snapshot.Items[0].MenuItemID())
	assert.True(t, snapshot.Items[0].UnitPrice().Equal(ordertest.Money(t, "12.50")), "unit price comes from the restaurants menu")
	assert.Equal(t, []order.ID{"order-1"}, env.store.RelationshipChanges(), "orders-consumer syncs the relationships of the new Order")
	assert.Equal(t, []string{"Bearer orders-token"}, restaurants.authorization, "restaurants sees the orders service token, not the BFF one")
}

func TestPlaceOrderEndToEndRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		restaurants *fakeMenu
		mutate      func(*ordersv1.PlaceOrderRequest)
		call        func(userVectors) context.Context
		code        codes.Code
		reason      string
	}{
		// AC-510: the BFF without a user assertion is a service, not a Customer.
		"bff without assertion": {openMenu(), nil,
			func(v userVectors) context.Context { return asBFF(v.DelegateToken, "") }, codes.PermissionDenied, "OPERATION_NOT_ALLOWED"},
		"restaurants unavailable": {&fakeMenu{err: status.Error(codes.Unavailable, "down")}, nil,
			nil, codes.Unavailable, "DEPENDENCY_UNAVAILABLE"},
		"unknown restaurant": {&fakeMenu{err: status.Error(codes.NotFound, "no restaurant")}, nil,
			nil, codes.NotFound, "RESTAURANT_NOT_FOUND"},
		"closed restaurant": {&fakeMenu{open: false}, nil,
			nil, codes.FailedPrecondition, "RESTAURANT_NOT_ACCEPTING_ORDERS"},
		"unknown menu item": {openMenu(), func(r *ordersv1.PlaceOrderRequest) { r.Items[0].MenuItemId = "missing" },
			nil, codes.FailedPrecondition, "ORDER_NOT_ORDERABLE"},
		"unavailable menu item": {openMenu(), func(r *ordersv1.PlaceOrderRequest) { r.Items[0].MenuItemId = "gyoza" },
			nil, codes.FailedPrecondition, "ORDER_NOT_ORDERABLE"},
	} {
		t.Run(name, func(t *testing.T) {
			env := newE2E(t, tc.restaurants)
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
	env := newE2E(t, openMenu())

	_, err := env.client.PlaceOrder(asBFF(env.vectors.NonDelegateToken, env.vectors.Tokens["valid"]), placeRequest())
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, placed := env.store.Snapshot("order-1")
	assert.False(t, placed)
}
