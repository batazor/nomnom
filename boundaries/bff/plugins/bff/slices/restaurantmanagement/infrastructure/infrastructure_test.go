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

	"buf.build/gen/go/nomnom/restaurants/grpc/go/nomnom/restaurants/v1/restaurantsv1grpc"
	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"
	catalog "github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/catalog/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/market"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/restaurantmanagement/applications"
)

const fixtures = "../../../../../../../tests/identity/fixtures"

// restaurantsServer keeps the opening state of one restaurant like restaurants:
// a repeat answers changed = false.
type restaurantsServer struct {
	restaurantsv1grpc.UnimplementedRestaurantsServiceServer
	open   bool
	err    error
	got    string
	market string
	seenMD metadata.MD
	hours  []*restaurantsv1.OpeningInterval
}

func (s *restaurantsServer) restaurant() *restaurantsv1.Restaurant {
	return &restaurantsv1.Restaurant{RestaurantId: s.got, Name: "Ночная пекарня", IsOpen: s.open}
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

func (s *restaurantsServer) ListOwnedRestaurants(ctx context.Context, request *restaurantsv1.ListOwnedRestaurantsRequest) (*restaurantsv1.ListOwnedRestaurantsResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	s.got = request.GetPageToken()
	s.market = request.GetMarketId()
	if s.err != nil {
		return nil, s.err
	}
	return &restaurantsv1.ListOwnedRestaurantsResponse{
		Restaurants:   []*restaurantsv1.Restaurant{{RestaurantId: "restaurant-2", Name: "Пельменная"}},
		NextPageToken: "restaurant-2",
	}, nil
}

func (s *restaurantsServer) SetOpeningHours(ctx context.Context, request *restaurantsv1.SetOpeningHoursRequest) (*restaurantsv1.SetOpeningHoursResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	s.got = request.GetRestaurantId()
	s.hours = request.GetOpeningHours()
	if s.err != nil {
		return nil, s.err
	}
	return &restaurantsv1.SetOpeningHoursResponse{Restaurant: s.restaurant(), Version: 4, Changed: true}, nil
}

func connect(t *testing.T, server *restaurantsServer) *Restaurants {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	restaurantsv1grpc.RegisterRestaurantsServiceServer(grpcServer, server)
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

// ownerContext is a request of owner-1 in Market berlin.
func ownerContext() context.Context {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(market.Metadata, "berlin"))
	return identity.WithUser(ctx, identity.User{ID: "owner-1"})
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
		Restaurant: applications.Restaurant{ID: "restaurant-1", Name: "Ночная пекарня", IsOpen: true},
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

// The list goes to restaurants on behalf of the user, who decides what it owns.
func TestListOwnedCarriesUserAssertion(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)

	page, err := restaurants.ListOwned(ownerContext(), 1, "restaurant-1")

	require.NoError(t, err)
	assert.Equal(t, "restaurant-1", server.got)
	assert.Equal(t, "berlin", server.market, "the list is of the request Market")
	assert.Equal(t, applications.OwnedPage{
		Restaurants: []applications.Restaurant{{ID: "restaurant-2", Name: "Пельменная"}},
		NextCursor:  "restaurant-2",
	}, page)
	assert.Len(t, server.seenMD.Get(identity.AssertionMetadata), 1)

	server.err = status.Error(codes.Unavailable, "down")
	_, err = restaurants.ListOwned(ownerContext(), 1, "")
	assert.Equal(t, codes.Unavailable, status.Code(errors.Unwrap(err)))
}

// FR-700: the whole schedule goes on behalf of the user; the rejected interval
// named by restaurants reaches the scenario.
func TestSetOpeningHoursCarriesTheScheduleAndNamesTheRejectedInterval(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)
	hours := []catalog.OpeningInterval{{Day: 5, Opens: "18:00", Closes: "02:00"}, {Day: 6, Opens: "01:00", Closes: "03:00"}}

	changed, err := restaurants.SetOpeningHours(ownerContext(), "restaurant-1", hours)
	require.NoError(t, err)
	assert.True(t, changed)
	require.Len(t, server.hours, 2)
	assert.Equal(t, int32(6), server.hours[1].GetDayOfWeek())
	assert.Equal(t, "01:00", server.hours[1].GetOpens())
	assert.Len(t, server.seenMD.Get(identity.AssertionMetadata), 1)

	server.err = status.Error(codes.InvalidArgument, "invalid opening hours: overlap at day 6 01:00-03:00")
	_, err = restaurants.SetOpeningHours(ownerContext(), "restaurant-1", hours)
	assert.Equal(t, applications.InvalidOpeningHours{Reason: "overlap", Day: 6, Opens: "01:00", Closes: "03:00"}, err)

	server.err = status.Error(codes.InvalidArgument, "restaurant_id is required")
	_, err = restaurants.SetOpeningHours(ownerContext(), "", hours)
	assert.ErrorIs(t, err, applications.ErrInvalidRestaurant)

	server.err = status.Error(codes.PermissionDenied, "not the owner")
	_, err = restaurants.SetOpeningHours(ownerContext(), "restaurant-1", hours)
	assert.ErrorIs(t, err, applications.ErrNotPermitted)
}
