package infrastructure

import (
	"context"
	"errors"
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
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
)

const fixtures = "../../../../../../tests/identity/fixtures"

// restaurantsServer keeps the opening state of one restaurant like restaurants:
// a repeat answers changed = false.
type restaurantsServer struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer
	open   bool
	err    error
	got    string
	seenMD metadata.MD
}

func (s *restaurantsServer) restaurant() *restaurantsv1.Restaurant {
	return &restaurantsv1.Restaurant{RestaurantId: s.got, Name: "Ночная пекарня", Address: "ул. Садовая, 8", IsOpen: s.open}
}

func (s *restaurantsServer) OpenRestaurant(ctx context.Context, request *restaurantsv1.OpenRestaurantRequest) (*restaurantsv1.OpenRestaurantResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	s.got = request.GetRestaurantId()
	if s.err != nil {
		return nil, s.err
	}
	changed := !s.open
	s.open = true
	return &restaurantsv1.OpenRestaurantResponse{Restaurant: s.restaurant(), Version: 2, Changed: changed}, nil
}

func (s *restaurantsServer) CloseRestaurant(ctx context.Context, request *restaurantsv1.CloseRestaurantRequest) (*restaurantsv1.CloseRestaurantResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	s.got = request.GetRestaurantId()
	if s.err != nil {
		return nil, s.err
	}
	changed := s.open
	s.open = false
	return &restaurantsv1.CloseRestaurantResponse{Restaurant: s.restaurant(), Version: 3, Changed: changed}, nil
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

func ownerContext() context.Context {
	return identity.WithUser(context.Background(), identity.User{ID: "owner-1"})
}

// The commands go to restaurants on behalf of the user: service token and
// user assertion (ADR 32).
func TestOpenAndCloseCarryServiceTokenAndUserAssertion(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)

	opened, err := restaurants.Open(ownerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, "restaurant-1", server.got)
	assert.Equal(t, applications.OpeningResult{
		Restaurant: applications.Restaurant{ID: "restaurant-1", Name: "Ночная пекарня", Address: "ул. Садовая, 8", IsOpen: true},
		Changed:    true,
	}, opened)
	assert.Equal(t, []string{"Bearer token-nomnom.restaurants"}, server.seenMD.Get("authorization"))
	assert.Len(t, server.seenMD.Get(identity.AssertionMetadata), 1)

	closed, err := restaurants.Close(ownerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.False(t, closed.Restaurant.IsOpen)
	assert.True(t, closed.Changed)
	assert.Len(t, server.seenMD.Get(identity.AssertionMetadata), 1)
}

// A repeat succeeds with changed = false.
func TestRepeatedOpeningIsNotAChange(t *testing.T) {
	server := &restaurantsServer{open: true}
	restaurants := connect(t, server)

	opened, err := restaurants.Open(ownerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.True(t, opened.Restaurant.IsOpen)
	assert.False(t, opened.Changed)

	server.open = false
	closed, err := restaurants.Close(ownerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.False(t, closed.Changed)
}

func TestOpeningNamesRefusals(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)

	for code, want := range map[codes.Code]error{
		codes.NotFound:         applications.ErrRestaurantNotFound,
		codes.PermissionDenied: applications.ErrNotPermitted,
		codes.InvalidArgument:  applications.ErrInvalidRestaurant,
	} {
		server.err = status.Error(code, "refused")
		_, err := restaurants.Open(ownerContext(), "restaurant-1")
		assert.ErrorIs(t, err, want, code)
		_, err = restaurants.Close(ownerContext(), "restaurant-1")
		assert.ErrorIs(t, err, want, code)
	}

	server.err = status.Error(codes.Unavailable, "spicedb is down")
	_, err := restaurants.Open(ownerContext(), "restaurant-1")
	require.Error(t, err)
	for _, refusal := range []error{applications.ErrRestaurantNotFound, applications.ErrNotPermitted, applications.ErrInvalidRestaurant} {
		assert.False(t, errors.Is(err, refusal))
	}
	assert.Equal(t, codes.Unavailable, status.Code(err), "the cause is kept for the log")
}
