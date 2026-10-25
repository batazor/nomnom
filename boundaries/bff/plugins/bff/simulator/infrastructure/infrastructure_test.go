package infrastructure

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	deliveryv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/delivery/v1"
	ordersv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/orders/v1"
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/simulator/applications"
)

const fixtures = "../../../../../../tests/identity/fixtures"

// state keeps the Simulator of one context like the service does.
type state struct {
	enabled bool
	err     error
	seenMD  metadata.MD
}

func (s *state) get(ctx context.Context) (bool, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	return s.enabled, s.err
}

func (s *state) set(ctx context.Context, enabled bool) (bool, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	if s.err != nil {
		return false, s.err
	}
	s.enabled = enabled
	return enabled, nil
}

type ordersServer struct {
	ordersv1.UnimplementedOrdersServiceServer
	state
}

func (s *ordersServer) GetSimulator(ctx context.Context, _ *ordersv1.GetSimulatorRequest) (*ordersv1.GetSimulatorResponse, error) {
	enabled, err := s.get(ctx)
	if err != nil {
		return nil, err
	}
	return &ordersv1.GetSimulatorResponse{Enabled: enabled}, nil
}

func (s *ordersServer) SetSimulatorEnabled(ctx context.Context, request *ordersv1.SetSimulatorEnabledRequest) (*ordersv1.SetSimulatorEnabledResponse, error) {
	enabled, err := s.set(ctx, request.GetEnabled())
	if err != nil {
		return nil, err
	}
	return &ordersv1.SetSimulatorEnabledResponse{Enabled: enabled}, nil
}

type restaurantsServer struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer
	state
}

func (s *restaurantsServer) GetSimulator(ctx context.Context, _ *restaurantsv1.GetSimulatorRequest) (*restaurantsv1.GetSimulatorResponse, error) {
	enabled, err := s.get(ctx)
	if err != nil {
		return nil, err
	}
	return &restaurantsv1.GetSimulatorResponse{Enabled: enabled}, nil
}

func (s *restaurantsServer) SetSimulatorEnabled(ctx context.Context, request *restaurantsv1.SetSimulatorEnabledRequest) (*restaurantsv1.SetSimulatorEnabledResponse, error) {
	enabled, err := s.set(ctx, request.GetEnabled())
	if err != nil {
		return nil, err
	}
	return &restaurantsv1.SetSimulatorEnabledResponse{Enabled: enabled}, nil
}

type deliveryServer struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	state
}

func (s *deliveryServer) GetSimulator(ctx context.Context, _ *deliveryv1.GetSimulatorRequest) (*deliveryv1.GetSimulatorResponse, error) {
	enabled, err := s.get(ctx)
	if err != nil {
		return nil, err
	}
	return &deliveryv1.GetSimulatorResponse{Enabled: enabled}, nil
}

func (s *deliveryServer) SetSimulatorEnabled(ctx context.Context, request *deliveryv1.SetSimulatorEnabledRequest) (*deliveryv1.SetSimulatorEnabledResponse, error) {
	enabled, err := s.set(ctx, request.GetEnabled())
	if err != nil {
		return nil, err
	}
	return &deliveryv1.SetSimulatorEnabledResponse{Enabled: enabled}, nil
}

func connect(t *testing.T, audience string, register func(*grpc.Server)) grpc.ClientConnInterface {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	register(grpcServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, audience), []byte("token-"+audience), 0o600))
	signer, err := identity.NewAssertionSigner(filepath.Join(fixtures, "bff-signing-key.pem"), "nomnom-bff-test-key")
	require.NoError(t, err)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(identity.UnaryClientInterceptor(audience, identity.ServiceTokens{Dir: dir}, signer)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func operatorContext() context.Context {
	return identity.WithUser(context.Background(), identity.User{ID: "operator-1"})
}

type contextUnderTest struct {
	audience  string
	state     *state
	simulator applications.Simulator
}

func contexts(t *testing.T) []contextUnderTest {
	t.Helper()
	orders, restaurants, delivery := &ordersServer{}, &restaurantsServer{}, &deliveryServer{}
	return []contextUnderTest{
		{"nomnom.orders", &orders.state, NewOrders(connect(t, "nomnom.orders", func(s *grpc.Server) { ordersv1.RegisterOrdersServiceServer(s, orders) }))},
		{"nomnom.restaurants", &restaurants.state, NewRestaurants(connect(t, "nomnom.restaurants", func(s *grpc.Server) { restaurantsv1.RegisterRestaurantsServiceServer(s, restaurants) }))},
		{"nomnom.delivery", &delivery.state, NewDelivery(connect(t, "nomnom.delivery", func(s *grpc.Server) { deliveryv1.RegisterDeliveryServiceServer(s, delivery) }))},
	}
}

// The Simulator RPCs go to each context on behalf of the user: service token
// and user assertion (ADR 32).
func TestSimulatorCarriesServiceTokenAndUserAssertion(t *testing.T) {
	for _, c := range contexts(t) {
		enabled, err := c.simulator.SetEnabled(operatorContext(), true)
		require.NoError(t, err, c.audience)
		assert.True(t, enabled, c.audience)
		assert.Equal(t, []string{"Bearer token-" + c.audience}, c.state.seenMD.Get("authorization"), c.audience)
		assert.Len(t, c.state.seenMD.Get(identity.AssertionMetadata), 1, c.audience)

		enabled, err = c.simulator.Enabled(operatorContext())
		require.NoError(t, err, c.audience)
		assert.True(t, enabled, c.audience)
		assert.Len(t, c.state.seenMD.Get(identity.AssertionMetadata), 1, c.audience)
	}
}

func TestSimulatorNamesRefusals(t *testing.T) {
	for _, c := range contexts(t) {
		c.state.err = status.Error(codes.PermissionDenied, "refused")
		_, err := c.simulator.Enabled(operatorContext())
		assert.ErrorIs(t, err, applications.ErrNotPermitted, c.audience)
		_, err = c.simulator.SetEnabled(operatorContext(), true)
		assert.ErrorIs(t, err, applications.ErrNotPermitted, c.audience)

		c.state.err = status.Error(codes.Unavailable, "spicedb is down")
		_, err = c.simulator.Enabled(operatorContext())
		assert.Equal(t, codes.Unavailable, status.Code(err), c.audience)
		assert.NotErrorIs(t, err, applications.ErrNotPermitted, c.audience)
	}
}
