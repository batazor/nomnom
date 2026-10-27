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

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/market"
)

const fixtures = "../../../../../../tests/identity/fixtures"

type restaurantsServer struct {
	restaurantsv1.UnimplementedRestaurantsServiceServer
	listed        *restaurantsv1.ListRestaurantsRequest
	restaurantErr error
	menuErr       error
	// withoutWaitTime answers GetRestaurant like a restaurants without FR-204.
	withoutWaitTime bool
	seenMD          metadata.MD
	menusOf         []string
}

func (s *restaurantsServer) ListRestaurants(ctx context.Context, request *restaurantsv1.ListRestaurantsRequest) (*restaurantsv1.ListRestaurantsResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	s.listed = request
	return &restaurantsv1.ListRestaurantsResponse{
		Restaurants: []*restaurantsv1.Restaurant{{
			RestaurantId: "restaurant-1", Name: "Pho Bar", IsOpen: true,
			WaitTime: &restaurantsv1.RestaurantWaitTime{Elevated: true, ExpectedWaitMinutes: 42},
		}},
		NextPageToken: "restaurant-1",
	}, nil
}

func (s *restaurantsServer) GetRestaurant(ctx context.Context, request *restaurantsv1.GetRestaurantRequest) (*restaurantsv1.GetRestaurantResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	if s.restaurantErr != nil {
		return nil, s.restaurantErr
	}
	if s.withoutWaitTime {
		return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{RestaurantId: request.GetRestaurantId(), Name: "Pho Bar"}}, nil
	}
	return &restaurantsv1.GetRestaurantResponse{Restaurant: &restaurantsv1.Restaurant{
		RestaurantId: request.GetRestaurantId(), Name: "Pho Bar", IsOpen: false,
		WaitTime: &restaurantsv1.RestaurantWaitTime{ExpectedWaitMinutes: 20},
	}}, nil
}

func (s *restaurantsServer) GetMenu(context.Context, *restaurantsv1.GetMenuRequest) (*restaurantsv1.GetMenuResponse, error) {
	if s.menuErr != nil {
		return nil, s.menuErr
	}
	return &restaurantsv1.GetMenuResponse{Items: []*restaurantsv1.MenuItem{{
		MenuItemId: "menu-gyoza", Name: "Гёдза", Description: "6 шт.",
		Price: &restaurantsv1.Money{Amount: "4.00", Currency: "EUR"}, Available: true,
	}}}, nil
}

// BatchGetMenus answers with the menu of restaurant-1 only, one without items
// for restaurant-2 and nothing for the others.
func (s *restaurantsServer) BatchGetMenus(ctx context.Context, request *restaurantsv1.BatchGetMenusRequest) (*restaurantsv1.BatchGetMenusResponse, error) {
	s.seenMD, _ = metadata.FromIncomingContext(ctx)
	s.menusOf = request.GetRestaurantIds()
	if s.menuErr != nil {
		return nil, s.menuErr
	}
	return &restaurantsv1.BatchGetMenusResponse{Menus: []*restaurantsv1.RestaurantMenu{
		{RestaurantId: "restaurant-1", Items: []*restaurantsv1.MenuItem{{
			MenuItemId: "menu-gyoza", Name: "Гёдза",
			Price: &restaurantsv1.Money{Amount: "4.00", Currency: "EUR"}, Available: true,
		}}},
		{RestaurantId: "restaurant-2"},
	}}, nil
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

// A customer in the context never reaches the public reads.
// customerContext is a request of customer-1 in Market berlin, as the Cosmo
// Router forwards it.
func customerContext() context.Context {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(market.Metadata, "berlin"))
	return identity.WithUser(ctx, identity.User{ID: "customer-1"})
}

func TestListAsksForOpenRestaurantsWithoutUser(t *testing.T) {
	server := &restaurantsServer{}

	page, err := connect(t, server).List(customerContext(), applications.PageRequest{Size: 1, Cursor: "restaurant-0"})

	require.NoError(t, err)
	assert.Equal(t, applications.Page{
		Restaurants: []applications.Restaurant{{
			ID: "restaurant-1", Name: "Pho Bar", IsOpen: true,
			WaitTime: applications.WaitTime{Elevated: true, ExpectedWaitMinutes: 42},
		}},
		NextCursor: "restaurant-1",
	}, page)
	assert.Equal(t, int32(1), server.listed.GetPageSize())
	assert.Equal(t, "restaurant-0", server.listed.GetPageToken())
	assert.False(t, server.listed.GetIncludeClosed())
	assert.Equal(t, "berlin", server.listed.GetMarketId(), "the list is of the request Market")
	assert.Equal(t, []string{"Bearer token-nomnom.restaurants"}, server.seenMD.Get("authorization"))
	assert.Empty(t, server.seenMD.Get(identity.AssertionMetadata), "public read carries no user")
}

// ADR 41: without a Market the list is refused before restaurants is called.
func TestListNeedsTheMarketOfTheRequest(t *testing.T) {
	server := &restaurantsServer{}
	_, err := connect(t, server).List(identity.WithUser(context.Background(), identity.User{ID: "customer-1"}), applications.PageRequest{Size: 1})

	refusal, ok := errors.AsType[*market.Refusal](err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, codes.InvalidArgument, refusal.Code)
	assert.Nil(t, server.listed)
}

func TestRestaurantAndMenuMapPublicData(t *testing.T) {
	server := &restaurantsServer{}
	restaurants := connect(t, server)

	restaurant, err := restaurants.Restaurant(customerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, applications.Restaurant{
		ID: "restaurant-1", Name: "Pho Bar",
		WaitTime: applications.WaitTime{ExpectedWaitMinutes: 20},
	}, restaurant)
	assert.Empty(t, server.seenMD.Get(identity.AssertionMetadata))

	menu, err := restaurants.Menu(customerContext(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, []applications.MenuItem{{
		ID: "menu-gyoza", Name: "Гёдза", Description: "6 шт.",
		Price: applications.Money{Amount: "4.00", Currency: "EUR"}, Available: true,
	}}, menu)
}

// A page of menus is one BatchGetMenus without a user; a published menu
// without items stays an empty menu, a restaurant without a menu is absent.
func TestMenusReadThePageInOneCall(t *testing.T) {
	server := &restaurantsServer{}

	menus, err := connect(t, server).Menus(customerContext(), []string{"restaurant-1", "restaurant-2", "restaurant-3"})

	require.NoError(t, err)
	assert.Equal(t, map[string][]applications.MenuItem{
		"restaurant-1": {{ID: "menu-gyoza", Name: "Гёдза", Price: applications.Money{Amount: "4.00", Currency: "EUR"}, Available: true}},
		"restaurant-2": {},
	}, menus)
	assert.Equal(t, []string{"restaurant-1", "restaurant-2", "restaurant-3"}, server.menusOf)
	assert.Empty(t, server.seenMD.Get(identity.AssertionMetadata), "public read carries no user")
}

func TestNotFoundIsNamed(t *testing.T) {
	server := &restaurantsServer{
		restaurantErr: status.Error(codes.NotFound, "no restaurant"),
		menuErr:       status.Error(codes.NotFound, "no menu"),
	}
	restaurants := connect(t, server)

	_, err := restaurants.Restaurant(context.Background(), "missing")
	assert.ErrorIs(t, err, applications.ErrRestaurantNotFound)
	_, err = restaurants.Menu(context.Background(), "missing")
	assert.ErrorIs(t, err, applications.ErrMenuNotFound)

	server.restaurantErr = status.Error(codes.Unavailable, "down")
	_, err = restaurants.Restaurant(context.Background(), "restaurant-1")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, applications.ErrRestaurantNotFound)
}

// restaurants FR-204: a restaurant without wait_time breaks the contract.
func TestRestaurantWithoutWaitTimeIsAnError(t *testing.T) {
	_, err := connect(t, &restaurantsServer{withoutWaitTime: true}).Restaurant(customerContext(), "restaurant-1")

	require.ErrorContains(t, err, "no wait_time")
}
