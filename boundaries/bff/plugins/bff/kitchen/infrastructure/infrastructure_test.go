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

var (
	receivedAt      = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	acceptedAt      = receivedAt.Add(time.Minute)
	expectedReadyAt = receivedAt.Add(16 * time.Minute)
	windowStart     = receivedAt.Add(2 * time.Hour)
	windowEnd       = windowStart.Add(15 * time.Minute)
	plannedReadyAt  = windowStart.Add(-20 * time.Minute)
)

// restaurantsServer answers the kitchen board and commands and records which
// calls carried a user assertion.
type restaurantsServer struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer
	mu    sync.Mutex
	err   error
	state restaurantsv1.RestaurantOrderState
	// emptyQueue answers StartNextPreparation without an order.
	emptyQueue bool
	// priority of ro-1; UNSPECIFIED means STANDARD unless unspecified is set.
	priority    restaurantsv1.OrderPriority
	unspecified bool
	calls       []string
	assertions  map[string]bool
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
	priority := s.priority
	if priority == restaurantsv1.OrderPriority_ORDER_PRIORITY_UNSPECIFIED && !s.unspecified {
		priority = restaurantsv1.OrderPriority_ORDER_PRIORITY_STANDARD
	}
	return &restaurantsv1.ListActiveRestaurantOrdersResponse{Orders: []*restaurantsv1.ActiveRestaurantOrder{{
		RestaurantOrder: &restaurantsv1.RestaurantOrder{
			RestaurantOrderId: "ro-1", OrderId: "order-1", RestaurantId: request.GetRestaurantId(), State: state,
			ExpectedReadyAt: timestamppb.New(expectedReadyAt), Priority: priority,
		},
		Items:      []*restaurantsv1.RestaurantOrderItem{{MenuItemId: "menu-pho", Quantity: 2}},
		ReceivedAt: timestamppb.New(receivedAt),
		AcceptedAt: timestamppb.New(acceptedAt),
	}, {
		RestaurantOrder: &restaurantsv1.RestaurantOrder{
			RestaurantOrderId: "ro-received", OrderId: "order-received", RestaurantId: request.GetRestaurantId(),
			State:    restaurantsv1.RestaurantOrderState_RESTAURANT_ORDER_STATE_RECEIVED,
			Priority: restaurantsv1.OrderPriority_ORDER_PRIORITY_PREMIUM,
			Schedule: &restaurantsv1.OrderSchedule{
				WindowStart: timestamppb.New(windowStart), WindowEnd: timestamppb.New(windowEnd),
			},
		},
		ReceivedAt: timestamppb.New(receivedAt),
	}}}, nil
}

func (s *restaurantsServer) ListScheduledOrders(ctx context.Context, request *restaurantsv1.ListScheduledOrdersRequest) (*restaurantsv1.ListScheduledOrdersResponse, error) {
	if err := s.record(ctx, "ListScheduledOrders:"+request.GetRestaurantId()); err != nil {
		return nil, err
	}
	return &restaurantsv1.ListScheduledOrdersResponse{Orders: []*restaurantsv1.ScheduledRestaurantOrder{{
		RestaurantOrderId: "ro-scheduled", OrderId: "order-scheduled",
		Items:    []*restaurantsv1.OrderRequestItem{{MenuItemId: "menu-pho", Quantity: 1}},
		Priority: restaurantsv1.OrderPriority_ORDER_PRIORITY_STANDARD,
		Schedule: &restaurantsv1.OrderSchedule{
			WindowStart: timestamppb.New(windowStart), WindowEnd: timestamppb.New(windowEnd), PlannedReadyAt: timestamppb.New(plannedReadyAt),
		},
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

func (s *restaurantsServer) StartNextPreparation(ctx context.Context, r *restaurantsv1.StartNextPreparationRequest) (*restaurantsv1.StartNextPreparationResponse, error) {
	if err := s.record(ctx, "StartNext:"+r.GetRestaurantId()+"/"+r.GetRequestId()); err != nil {
		return nil, err
	}
	if s.emptyQueue {
		return &restaurantsv1.StartNextPreparationResponse{}, nil
	}
	return &restaurantsv1.StartNextPreparationResponse{RestaurantOrder: &restaurantsv1.RestaurantOrder{RestaurantOrderId: "ro-2"}}, nil
}

func (s *restaurantsServer) MarkRestaurantOrderAssembled(ctx context.Context, r *restaurantsv1.MarkRestaurantOrderAssembledRequest) (*restaurantsv1.MarkRestaurantOrderAssembledResponse, error) {
	return &restaurantsv1.MarkRestaurantOrderAssembledResponse{}, s.record(ctx, "Assembled:"+r.GetRestaurantId()+"/"+r.GetRestaurantOrderId())
}

func (s *restaurantsServer) MarkRestaurantOrderReady(ctx context.Context, r *restaurantsv1.MarkRestaurantOrderReadyRequest) (*restaurantsv1.MarkRestaurantOrderReadyResponse, error) {
	return &restaurantsv1.MarkRestaurantOrderReadyResponse{}, s.record(ctx, "Ready:"+r.GetRestaurantId()+"/"+r.GetRestaurantOrderId())
}

func (s *restaurantsServer) GetCookPool(ctx context.Context, r *restaurantsv1.GetCookPoolRequest) (*restaurantsv1.GetCookPoolResponse, error) {
	if err := s.record(ctx, "CookPool:"+r.GetRestaurantId()); err != nil {
		return nil, err
	}
	return &restaurantsv1.GetCookPoolResponse{Size: 2, OnLine: 1, Busy: 1, Queued: 3, Cooks: []*restaurantsv1.Cook{
		{CookId: "cook-1", RestaurantId: r.GetRestaurantId(), Name: "Айгуль", OnLine: true, CurrentRestaurantOrderId: "ro-1"},
		{CookId: "cook-2", RestaurantId: r.GetRestaurantId(), Name: "Олег"},
	}}, nil
}

func (s *restaurantsServer) HireCook(ctx context.Context, r *restaurantsv1.HireCookRequest) (*restaurantsv1.HireCookResponse, error) {
	return &restaurantsv1.HireCookResponse{}, s.record(ctx, "Hire:"+r.GetRestaurantId()+"/"+r.GetName())
}

func (s *restaurantsServer) PutCookOnLine(ctx context.Context, r *restaurantsv1.PutCookOnLineRequest) (*restaurantsv1.PutCookOnLineResponse, error) {
	return &restaurantsv1.PutCookOnLineResponse{}, s.record(ctx, "OnLine:"+r.GetRestaurantId()+"/"+r.GetCookId())
}

func (s *restaurantsServer) TakeCookOffLine(ctx context.Context, r *restaurantsv1.TakeCookOffLineRequest) (*restaurantsv1.TakeCookOffLineResponse, error) {
	return &restaurantsv1.TakeCookOffLineResponse{}, s.record(ctx, "OffLine:"+r.GetRestaurantId()+"/"+r.GetCookId())
}

func (s *restaurantsServer) RetireCook(ctx context.Context, r *restaurantsv1.RetireCookRequest) (*restaurantsv1.RetireCookResponse, error) {
	return &restaurantsv1.RetireCookResponse{}, s.record(ctx, "Retire:"+r.GetRestaurantId()+"/"+r.GetCookId())
}

func (s *restaurantsServer) GetRestaurant(ctx context.Context, r *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	if err := s.record(ctx, "GetRestaurant"); err != nil {
		return nil, err
	}
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: r.GetRestaurantId(), Name: "Кухня №8", IsOpen: true}}, nil
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
		Lines:           []applications.ActiveOrderLine{{MenuItemID: "menu-pho", Quantity: 2}},
		ReceivedAt:      receivedAt,
		AcceptedAt:      &acceptedAt,
		ExpectedReadyAt: &expectedReadyAt,
		Priority:        applications.PriorityStandard,
	}, {
		ID: "ro-received", OrderID: "order-received", Stage: applications.StageReceived,
		Lines:          []applications.ActiveOrderLine{},
		ReceivedAt:     receivedAt,
		Priority:       applications.PriorityPremium,
		DeliveryWindow: &applications.Window{Start: windowStart, End: windowEnd},
	}}, orders, "an order before Acceptance has neither acceptedAt nor expectedReadyAt")
	scheduled, err := restaurants.ScheduledOrders(cookContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, []applications.ScheduledOrder{{
		ID: "ro-scheduled", OrderID: "order-scheduled",
		Lines:          []applications.ActiveOrderLine{{MenuItemID: "menu-pho", Quantity: 1}},
		Priority:       applications.PriorityStandard,
		DeliveryWindow: applications.Window{Start: windowStart, End: windowEnd},
		PlannedReadyAt: plannedReadyAt,
	}}, scheduled)
	queue, err := restaurants.QueueOrder(cookContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"ro-2", "ro-3"}, queue)
	restaurant, err := restaurants.Restaurant(cookContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}, restaurant)
	names, err := restaurants.MenuItemNames(cookContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"menu-pho": "Фо бо"}, names)

	assert.Equal(t, map[string]bool{
		"ListActiveRestaurantOrders:restaurant-1": true,
		"GetKitchenQueue:restaurant-1":            true,
		"ListScheduledOrders:restaurant-1":        true,
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
		_, err = restaurants.ScheduledOrders(cookContext(), "restaurant-1")
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
	for _, action := range []applications.Action{applications.ActionAccept, applications.ActionReject, applications.ActionMarkAssembled, applications.ActionMarkReady} {
		require.NoError(t, restaurants.Apply(cookContext(), "restaurant-1", "ro-1", action))
	}
	assert.Equal(t, []string{"Accept:restaurant-1/ro-1", "Reject:restaurant-1/ro-1", "Assembled:restaurant-1/ro-1", "Ready:restaurant-1/ro-1"}, server.calls)
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

// FR-225: a start of preparation without a free cook on line.
func TestStartWithoutAFreeCookIsNamed(t *testing.T) {
	server := &restaurantsServer{err: status.Error(codes.ResourceExhausted, "no cook")}
	_, err := connect(t, server).StartNext(cookContext(), "restaurant-1", "attempt-1")
	assert.ErrorIs(t, err, applications.ErrNoCookAvailable)
}

// FR-225: the queue names the order; an empty queue starts nothing.
func TestStartNextGoesOnBehalfOfTheUser(t *testing.T) {
	server := &restaurantsServer{}
	started, err := connect(t, server).StartNext(cookContext(), "restaurant-1", "attempt-1")
	require.NoError(t, err)
	assert.Equal(t, "ro-2", started)
	assert.True(t, server.assertions["StartNext:restaurant-1/attempt-1"])

	empty, err := connect(t, &restaurantsServer{emptyQueue: true}).StartNext(cookContext(), "restaurant-1", "attempt-2")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// FR-655: restaurants always names the priority of an order.
func TestBoardRejectsAnUnspecifiedPriority(t *testing.T) {
	_, err := connect(t, &restaurantsServer{unspecified: true}).ActiveOrders(cookContext(), "restaurant-1")
	assert.ErrorContains(t, err, "unexpected priority")
}

func TestCookPoolIsReadOnBehalfOfTheUser(t *testing.T) {
	server := &restaurantsServer{}
	pool, err := connect(t, server).CookPool(cookContext(), "restaurant-1")

	require.NoError(t, err)
	assert.Equal(t, applications.CookPool{Size: 2, OnLine: 1, Busy: 1, Queued: 3, Cooks: []applications.Cook{
		{ID: "cook-1", Name: "Айгуль", OnLine: true, CurrentKitchenOrderID: "ro-1"},
		{ID: "cook-2", Name: "Олег"},
	}}, pool)
	assert.True(t, server.assertions["CookPool:restaurant-1"])

	server.err = status.Error(codes.PermissionDenied, "no")
	_, err = connect(t, server).CookPool(cookContext(), "restaurant-1")
	assert.ErrorIs(t, err, applications.ErrKitchenNotFound)
}

func TestCookPoolCommandsNameTheirRefusals(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)
	require.NoError(t, restaurants.Hire(cookContext(), "restaurant-1", "Марат"))
	require.NoError(t, restaurants.SetOnLine(cookContext(), "restaurant-1", "cook-2", true))
	require.NoError(t, restaurants.SetOnLine(cookContext(), "restaurant-1", "cook-2", false))
	require.NoError(t, restaurants.Retire(cookContext(), "restaurant-1", "cook-2"))
	assert.Equal(t, []string{"Hire:restaurant-1/Марат", "OnLine:restaurant-1/cook-2", "OffLine:restaurant-1/cook-2", "Retire:restaurant-1/cook-2"}, server.calls)

	refused := func(code codes.Code, call func() error, want error) {
		server.err = status.Error(code, "refused")
		assert.ErrorIs(t, call(), want, code)
	}
	refused(codes.InvalidArgument, func() error { return restaurants.Hire(cookContext(), "restaurant-1", " ") }, applications.ErrInvalidCookName)
	refused(codes.FailedPrecondition, func() error { return restaurants.SetOnLine(cookContext(), "restaurant-1", "cook-1", true) }, applications.ErrCookRetired)
	refused(codes.FailedPrecondition, func() error { return restaurants.SetOnLine(cookContext(), "restaurant-1", "cook-1", false) }, applications.ErrCookBusy)
	refused(codes.FailedPrecondition, func() error { return restaurants.Retire(cookContext(), "restaurant-1", "cook-1") }, applications.ErrCookOnLine)
	refused(codes.NotFound, func() error { return restaurants.Retire(cookContext(), "restaurant-1", "cook-9") }, applications.ErrCookNotFound)
	refused(codes.PermissionDenied, func() error { return restaurants.Retire(cookContext(), "restaurant-1", "cook-1") }, applications.ErrNotPermitted)
}
