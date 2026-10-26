package cellrouter

import (
	"context"
	"errors"
	"net"
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

	controlplanev1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/controlplane/v1"
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
)

const (
	gatewayA = "gateway-a:8080"
	gatewayB = "gateway-b:8080"
)

// controlPlane serves the Placement Map; err makes the next calls fail.
type controlPlane struct {
	controlplanev1.UnimplementedControlPlaneServiceServer
	mu         sync.Mutex
	placements []*controlplanev1.Placement
	err        error
}

func (c *controlPlane) set(placements []*controlplanev1.Placement, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.placements, c.err = placements, err
}

func (c *controlPlane) GetPlacementMap(context.Context, *controlplanev1.GetPlacementMapRequest) (*controlplanev1.GetPlacementMapResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	return &controlplanev1.GetPlacementMapResponse{Placements: c.placements}, nil
}

// cell is the restaurants context of one Cell: it names itself in the answer
// and records the metadata it received.
type cell struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer
	name   string
	mu     sync.Mutex
	seenMD metadata.MD
}

func (c *cell) GetRestaurant(ctx context.Context, _ *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	c.mu.Lock()
	c.seenMD = md
	c.mu.Unlock()
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{Name: c.name}}, nil
}

func listen(t *testing.T, register func(*grpc.Server)) *bufconn.Listener {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener
}

func connTo(t *testing.T, listener *bufconn.Listener) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	return conn
}

// cells dials the bufconn listener of a gateway address and counts the dials
// per address and audience.
type cells struct {
	t         *testing.T
	listeners map[string]*bufconn.Listener
	mu        sync.Mutex
	dials     map[connKey]int
}

func (c *cells) dial(address, audience string) (*grpc.ClientConn, error) {
	listener, ok := c.listeners[address]
	if !ok {
		return nil, errors.New("no gateway at " + address)
	}
	c.mu.Lock()
	c.dials[connKey{address: address, audience: audience}]++
	c.mu.Unlock()
	return connTo(c.t, listener), nil
}

type fixture struct {
	controlPlane *controlPlane
	placements   *Placements
	router       *Router
	cells        *cells
	a, b         *cell
}

func newFixture(t *testing.T, ttl time.Duration) *fixture {
	t.Helper()
	f := &fixture{controlPlane: &controlPlane{}, a: &cell{name: "cell-a"}, b: &cell{name: "cell-b"}}
	f.controlPlane.set([]*controlplanev1.Placement{
		{MarketId: "berlin", CellId: "cell-a", GatewayAddress: gatewayA},
		{MarketId: "hamburg", CellId: "cell-a", GatewayAddress: gatewayA},
		{MarketId: "warsaw", CellId: "cell-b", GatewayAddress: gatewayB},
	}, nil)
	controlPlaneConn := connTo(t, listen(t, func(s *grpc.Server) { controlplanev1.RegisterControlPlaneServiceServer(s, f.controlPlane) }))
	t.Cleanup(func() { _ = controlPlaneConn.Close() })
	placements, err := NewPlacements(controlPlaneConn, ttl)
	require.NoError(t, err)
	f.placements = placements
	f.cells = &cells{t: t, dials: map[connKey]int{}, listeners: map[string]*bufconn.Listener{
		gatewayA: listen(t, func(s *grpc.Server) { restaurantsv1.RegisterRestaurantsServiceServer(s, f.a) }),
		gatewayB: listen(t, func(s *grpc.Server) { restaurantsv1.RegisterRestaurantsServiceServer(s, f.b) }),
	}}
	f.router = NewRouter(placements, f.cells.dial)
	t.Cleanup(func() { _ = f.router.Close() })
	return f
}

func inMarket(values ...string) context.Context {
	md := metadata.MD{}
	for _, value := range values {
		md.Append(MarketMetadata, value)
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

func (f *fixture) restaurantFrom(ctx context.Context, audience string) (string, error) {
	response, err := restaurantsv1.NewRestaurantsServiceClient(f.router.Conn(audience)).
		GetRestaurant(ctx, &restaurantsv1.GetRestaurantRequest{RestaurantId: "restaurant-1"})
	return response.GetRestaurant().GetName(), err
}

func refusalCode(t *testing.T, err error) codes.Code {
	t.Helper()
	refusal, ok := errors.AsType[*Refusal](err)
	require.True(t, ok, "want a Refusal, got %v", err)
	// Not a gRPC status: adapters must not read it as the owner's answer.
	assert.Equal(t, codes.Unknown, status.Code(err))
	assert.Equal(t, refusal.Code, status.Code(refusal.Err()))
	return refusal.Code
}

func TestCallsGoToTheCellOfTheMarket(t *testing.T) {
	f := newFixture(t, time.Hour)
	require.NoError(t, f.placements.Refresh(context.Background()))

	name, err := f.restaurantFrom(inMarket("berlin"), "nomnom.restaurants")
	require.NoError(t, err)
	assert.Equal(t, "cell-a", name)

	name, err = f.restaurantFrom(inMarket("warsaw"), "nomnom.restaurants")
	require.NoError(t, err)
	assert.Equal(t, "cell-b", name)
}

// The Market is read from the incoming metadata and is not forwarded to
// contexts as outgoing metadata.
func TestMarketIsNotForwardedToTheContext(t *testing.T) {
	f := newFixture(t, time.Hour)
	require.NoError(t, f.placements.Refresh(context.Background()))

	_, err := f.restaurantFrom(inMarket("berlin"), "nomnom.restaurants")
	require.NoError(t, err)
	assert.Empty(t, f.a.seenMD.Get(MarketMetadata))
}

func TestMarketHeaderMustBeOneValidSlug(t *testing.T) {
	f := newFixture(t, time.Hour)
	require.NoError(t, f.placements.Refresh(context.Background()))

	for name, ctx := range map[string]context.Context{
		"missing":        context.Background(),
		"twice":          inMarket("berlin", "warsaw"),
		"empty":          inMarket(""),
		"upper case":     inMarket("Berlin"),
		"leading digit":  inMarket("1berlin"),
		"one character":  inMarket("b"),
		"too long":       inMarket("a23456789012345678901234567890123"),
		"comma-joined":   inMarket("berlin,warsaw"),
		"with separator": inMarket("berlin_1"),
	} {
		_, err := f.restaurantFrom(ctx, "nomnom.restaurants")
		assert.Equal(t, codes.InvalidArgument, refusalCode(t, err), name)
	}
	assert.Empty(t, f.cells.dials, "a refused request dials no Cell")
}

func TestMarketFromAcceptsTheSlug(t *testing.T) {
	market, err := MarketFrom(inMarket("frankfurt-am-main"))
	require.NoError(t, err)
	assert.Equal(t, "frankfurt-am-main", market)
}

func TestMarketWithoutPlacementIsNotFound(t *testing.T) {
	f := newFixture(t, time.Hour)
	require.NoError(t, f.placements.Refresh(context.Background()))

	_, err := f.restaurantFrom(inMarket("paris"), "nomnom.restaurants")
	assert.Equal(t, codes.NotFound, refusalCode(t, err))
}

func TestCallsBeforeTheFirstLoadAreUnavailable(t *testing.T) {
	f := newFixture(t, time.Hour)

	_, err := f.restaurantFrom(inMarket("berlin"), "nomnom.restaurants")
	assert.Equal(t, codes.Unavailable, refusalCode(t, err))
}

// Static stability (ADR 41): a failed or invalid refresh keeps the last map.
func TestFailedRefreshKeepsTheLastMap(t *testing.T) {
	f := newFixture(t, time.Hour)
	require.NoError(t, f.placements.Refresh(context.Background()))

	f.controlPlane.set(nil, status.Error(codes.Unavailable, "control plane is down"))
	assert.Error(t, f.placements.Refresh(context.Background()))
	name, err := f.restaurantFrom(inMarket("warsaw"), "nomnom.restaurants")
	require.NoError(t, err)
	assert.Equal(t, "cell-b", name)

	for _, invalid := range [][]*controlplanev1.Placement{
		{{MarketId: "warsaw", GatewayAddress: gatewayA}, {MarketId: "warsaw", GatewayAddress: gatewayB}},
		{{MarketId: "warsaw"}},
		{{MarketId: "Warsaw", GatewayAddress: gatewayA}},
	} {
		f.controlPlane.set(invalid, nil)
		assert.Error(t, f.placements.Refresh(context.Background()))
		name, err := f.restaurantFrom(inMarket("warsaw"), "nomnom.restaurants")
		require.NoError(t, err)
		assert.Equal(t, "cell-b", name)
	}
}

// A successful refresh replaces the map: a Market placed later is routed.
func TestRefreshReplacesTheMap(t *testing.T) {
	f := newFixture(t, time.Hour)
	require.NoError(t, f.placements.Refresh(context.Background()))
	f.controlPlane.set([]*controlplanev1.Placement{{MarketId: "paris", CellId: "cell-b", GatewayAddress: gatewayB}}, nil)
	require.NoError(t, f.placements.Refresh(context.Background()))

	name, err := f.restaurantFrom(inMarket("paris"), "nomnom.restaurants")
	require.NoError(t, err)
	assert.Equal(t, "cell-b", name)
	_, err = f.restaurantFrom(inMarket("berlin"), "nomnom.restaurants")
	assert.Equal(t, codes.NotFound, refusalCode(t, err))
}

// One connection per gateway address and audience, shared by the Markets of
// one Cell.
func TestConnectionsAreCachedPerGatewayAndAudience(t *testing.T) {
	f := newFixture(t, time.Hour)
	require.NoError(t, f.placements.Refresh(context.Background()))

	for _, market := range []string{"berlin", "hamburg", "berlin", "warsaw"} {
		_, err := f.restaurantFrom(inMarket(market), "nomnom.restaurants")
		require.NoError(t, err)
	}
	// Another audience of the same Cell gets its own connection.
	_, err := f.restaurantFrom(inMarket("berlin"), "nomnom.orders")
	require.NoError(t, err)

	assert.Equal(t, map[connKey]int{
		{address: gatewayA, audience: "nomnom.restaurants"}: 1,
		{address: gatewayB, audience: "nomnom.restaurants"}: 1,
		{address: gatewayA, audience: "nomnom.orders"}:      1,
	}, f.cells.dials)
}

func TestStartLoadsTheMapInTheBackgroundUntilStop(t *testing.T) {
	f := newFixture(t, 10*time.Millisecond)
	f.controlPlane.set(nil, status.Error(codes.Unavailable, "control plane is starting"))
	f.placements.Start()

	_, err := f.restaurantFrom(inMarket("berlin"), "nomnom.restaurants")
	assert.Equal(t, codes.Unavailable, refusalCode(t, err))

	f.controlPlane.set([]*controlplanev1.Placement{{MarketId: "berlin", CellId: "cell-a", GatewayAddress: gatewayA}}, nil)
	require.Eventually(t, func() bool {
		_, err := f.placements.Gateway("berlin")
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	f.placements.Stop()
}

func TestPlacementTTLMustBePositive(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		_, err := NewPlacements(nil, ttl)
		assert.Error(t, err, ttl)
	}
}
