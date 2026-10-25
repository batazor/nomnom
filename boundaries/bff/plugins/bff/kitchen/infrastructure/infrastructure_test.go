package infrastructure

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
)

const fixtures = "../../../../../../tests/identity/fixtures"

var receivedAt = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// restaurantsServer answers the kitchen board and commands and records which
// calls carried a user assertion.
type restaurantsServer struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer
	mu         sync.Mutex
	err        error
	state      restaurantsv1.RestaurantOrderState
	calls      []string
	assertions map[string]bool
}

func (s *restaurantsServer) record(ctx context.Context, call string) error {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
	if s.assertions == nil {
		s.assertions = map[string]bool{}
	}
	s.assertions[call] = len(md.Get(identity.AssertionMetadata)) == 1
	return s.err
}

func (s *restaurantsServer) ListActiveRestaurantOrders(ctx context.Context, request *restaurantsv1.ListActiveRestaurantOrdersRequest) (*restaurantsv1.ListActiveRestaurantOrdersResponse, error) {
	if err := s.record(ctx, "ListActiveRestaurantOrders:"+request.GetRestaurantId()); err != nil {
		return nil, err
	}
	state := s.state
	if state == restaurantsv1.RestaurantOrderState_RESTAURANT_ORDER_STATE_UNSPECIFIED {
		state = restaurantsv1.RestaurantOrderState_RESTAURANT_ORDER_STATE_PREPARING
	}
	return &restaurantsv1.ListActiveRestaurantOrdersResponse{Orders: []*restaurantsv1.ActiveRestaurantOrder{{
		RestaurantOrder: &restaurantsv1.RestaurantOrder{RestaurantOrderId: "ro-1", OrderId: "order-1", RestaurantId: request.GetRestaurantId(), State: state},
		Items:           []*restaurantsv1.RestaurantOrderItem{{MenuItemId: "menu-pho", Quantity: 2}},
		ReceivedAt:      timestamppb.New(receivedAt),
	}}}, nil
}

func (s *restaurantsServer) GetKitchenQueue(ctx context.Context, request *restaurantsv1.GetKitchenQueueRequest) (*restaurantsv1.GetKitchenQueueResponse, error) {
	if err := s.record(ctx, "GetKitchenQueue:"+request.GetRestaurantId()); err != nil {
		return nil, err
	}
	return &restaurantsv1.GetKitchenQueueResponse{Entries: []*restaurantsv1.KitchenQueueEntry{
		{RestaurantOrder: &restaurantsv1.RestaurantOrder{RestaurantOrderId: "ro-2"}},
		{RestaurantOrder: &restaurantsv1.RestaurantOrder{RestaurantOrderId: "ro-3"}},
	}}, nil
}

func (s *restaurantsServer) AcceptRestaurantOrder(ctx context.Context, r *restaurantsv1.AcceptRestaurantOrderRequest) (*restaurantsv1.AcceptRestaurantOrderResponse, error) {
	return &restaurantsv1.AcceptRestaurantOrderResponse{}, s.record(ctx, "Accept:"+r.GetRestaurantId()+"/"+r.GetRestaurantOrderId())
}

func (s *restaurantsServer) RejectRestaurantOrder(ctx context.Context, r *restaurantsv1.RejectRestaurantOrderRequest) (*restaurantsv1.RejectRestaurantOrderResponse, error) {
	return &restaurantsv1.RejectRestaurantOrderResponse{}, s.record(ctx, "Reject:"+r.GetRestaurantId()+"/"+r.GetRestaurantOrderId())
}

func (s *restaurantsServer) StartPreparation(ctx context.Context, r *restaurantsv1.StartPreparationRequest) (*restaurantsv1.StartPreparationResponse, error) {
	return &restaurantsv1.StartPreparationResponse{}, s.record(ctx, "Start:"+r.GetRestaurantId()+"/"+r.GetRestaurantOrderId())
}

func (s *restaurantsServer) MarkRestaurantOrderAssembled(ctx context.Context, r *restaurantsv1.MarkRestaurantOrderAssembledRequest) (*restaurantsv1.MarkRestaurantOrderAssembledResponse, error) {
	return &restaurantsv1.MarkRestaurantOrderAssembledResponse{}, s.record(ctx, "Assembled:"+r.GetRestaurantId()+"/"+r.GetRestaurantOrderId())
}

func (s *restaurantsServer) MarkRestaurantOrderReady(ctx context.Context, r *restaurantsv1.MarkRestaurantOrderReadyRequest) (*restaurantsv1.MarkRestaurantOrderReadyResponse, error) {
	return &restaurantsv1.MarkRestaurantOrderReadyResponse{}, s.record(ctx, "Ready:"+r.GetRestaurantId()+"/"+r.GetRestaurantOrderId())
}

func (s *restaurantsServer) GetRestaurant(ctx context.Context, r *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	if err := s.record(ctx, "GetRestaurant"); err != nil {
		return nil, err
	}
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: r.GetRestaurantId(), Name: "Кухня №8", Address: "ул. Садовая, 8", IsOpen: true}}, nil
}

func (s *restaurantsServer) GetMenu(ctx context.Context, _ *restaurantsv1.GetMenuRequest) (*restaurantsv1.GetMenuResponse, error) {
	if err := s.record(ctx, "GetMenu"); err != nil {
		return nil, err
	}
	return &restaurantsv1.GetMenuResponse{Items: []*restaurantsv1.MenuItem{{MenuItemId: "menu-pho", Name: "Фо бо"}}}, nil
}

func connect(t *testing.T, server *restaurantsServer) *Restaurants {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	restaurantsv1.RegisterRestaurantsServiceServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nomnom.restaurants"), []byte("token-nomnom.restaurants"), 0o600))
	signer, err := identity.NewAssertionSigner(filepath.Join(fixtures, "bff-signing-key.pem"), "nomnom-bff-test-key")
	require.NoError(t, err)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(identity.UnaryClientInterceptor("nomnom.restaurants", identity.ServiceTokens{Dir: dir}, signer)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return NewRestaurants(conn)
}

func cookContext() context.Context {
	return identity.WithUser(context.Background(), identity.User{ID: "cook-1"})
}

// The board goes on behalf of the user; the public reads go without it.
func TestBoardCarriesTheUserAndPublicReadsDoNot(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)

	orders, err := restaurants.ActiveOrders(cookContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, []applications.ActiveOrder{{
		ID: "ro-1", OrderID: "order-1", Stage: applications.StagePreparing,
		Lines:      []applications.ActiveOrderLine{{MenuItemID: "menu-pho", Quantity: 2}},
		ReceivedAt: receivedAt,
	}}, orders)
	queue, err := restaurants.QueueOrder(cookContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"ro-2", "ro-3"}, queue)
	restaurant, err := restaurants.Restaurant(cookContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", Address: "ул. Садовая, 8", IsOpen: true}, restaurant)
	names, err := restaurants.MenuItemNames(cookContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"menu-pho": "Фо бо"}, names)

	assert.Equal(t, map[string]bool{
		"ListActiveRestaurantOrders:restaurant-1": true,
		"GetKitchenQueue:restaurant-1":            true,
		"GetRestaurant":                           false,
		"GetMenu":                                 false,
	}, server.assertions)
}

func TestBoardHidesMissingAndForbiddenKitchens(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)
	for _, code := range []codes.Code{codes.NotFound, codes.PermissionDenied} {
		server.err = status.Error(code, "refused")
		_, err := restaurants.ActiveOrders(cookContext(), "restaurant-1")
		assert.ErrorIs(t, err, applications.ErrKitchenNotFound, code)
		_, err = restaurants.QueueOrder(cookContext(), "restaurant-1")
		assert.ErrorIs(t, err, applications.ErrKitchenNotFound, code)
	}
	server.err = status.Error(codes.NotFound, "no restaurant")
	_, err := restaurants.Restaurant(cookContext(), "restaurant-1")
	assert.ErrorIs(t, err, applications.ErrKitchenNotFound)
	names, err := restaurants.MenuItemNames(cookContext(), "restaurant-1")
	require.NoError(t, err, "a restaurant without a menu shows neutral labels")
	assert.Empty(t, names)

	server.err = status.Error(codes.Unavailable, "down")
	_, err = restaurants.ActiveOrders(cookContext(), "restaurant-1")
	assert.Equal(t, codes.Unavailable, status.Code(errors.Unwrap(err)))
}

func TestBoardRejectsStatesOutsideTheKitchen(t *testing.T) {
	server := &restaurantsServer{state: restaurantsv1.RestaurantOrderState_RESTAURANT_ORDER_STATE_REJECTED}
	_, err := connect(t, server).ActiveOrders(cookContext(), "restaurant-1")
	assert.Error(t, err)
}

func TestCommandsGoToTheirRPCOnBehalfOfTheUser(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)
	for _, action := range []applications.Action{applications.ActionAccept, applications.ActionReject, applications.ActionStartPreparation, applications.ActionMarkAssembled, applications.ActionMarkReady} {
		require.NoError(t, restaurants.Apply(cookContext(), "restaurant-1", "ro-1", action))
	}
	assert.Equal(t, []string{"Accept:restaurant-1/ro-1", "Reject:restaurant-1/ro-1", "Start:restaurant-1/ro-1", "Assembled:restaurant-1/ro-1", "Ready:restaurant-1/ro-1"}, server.calls)
	for call, asserted := range server.assertions {
		assert.True(t, asserted, call)
	}
	assert.ErrorIs(t, restaurants.Apply(cookContext(), "restaurant-1", "ro-1", "cook"), applications.ErrUnknownAction)
}

func TestCommandsNameRefusals(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)
	for code, want := range map[codes.Code]error{
		codes.NotFound:           applications.ErrOrderNotFound,
		codes.PermissionDenied:   applications.ErrNotPermitted,
		codes.FailedPrecondition: applications.ErrStageConflict,
		codes.InvalidArgument:    applications.ErrInvalidCommand,
	} {
		server.err = status.Error(code, "refused")
		assert.ErrorIs(t, restaurants.Apply(cookContext(), "restaurant-1", "ro-1", applications.ActionAccept), want, code)
	}
	server.err = status.Error(codes.Unavailable, "down")
	err := restaurants.Apply(cookContext(), "restaurant-1", "ro-1", applications.ActionAccept)
	assert.Equal(t, codes.Unavailable, status.Code(errors.Unwrap(err)))
}
