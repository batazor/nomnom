package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

type fakeListRestaurants struct {
	page  applications.RestaurantPage
	err   error
	first int
	after string
}

func (f *fakeListRestaurants) Handle(_ context.Context, first int, after string) (applications.RestaurantPage, error) {
	f.first, f.after = first, after
	return f.page, f.err
}

type fakeGetRestaurant struct {
	view applications.RestaurantView
	err  error
}

func (f *fakeGetRestaurant) Handle(context.Context, string) (applications.RestaurantView, error) {
	return f.view, f.err
}

var phoBar = applications.Restaurant{ID: "restaurant-1", Name: "Pho Bar", Address: "ул. Садовая, 8", IsOpen: true}

// Catalog resolvers need no BFF identity in the service and no access token.
func catalogService(list *fakeListRestaurants, get *fakeGetRestaurant) *BffService {
	return &BffService{catalog: &restaurantCatalog{listRestaurants: list, getRestaurant: get}}
}

func TestQueryRestaurantsMapsThePageWithoutAccessToken(t *testing.T) {
	list := &fakeListRestaurants{page: applications.RestaurantPage{
		Edges:       []applications.RestaurantEdge{{Cursor: "restaurant-1", Restaurant: phoBar}},
		HasNextPage: true,
		EndCursor:   "restaurant-1",
	}}

	response, err := catalogService(list, &fakeGetRestaurant{}).QueryRestaurants(context.Background(),
		&service.QueryRestaurantsRequest{First: 1, After: wrapperspb.String("restaurant-0")})

	require.NoError(t, err)
	assert.Equal(t, 1, list.first)
	assert.Equal(t, "restaurant-0", list.after)
	connection := response.GetRestaurants()
	require.Len(t, connection.GetEdges(), 1)
	assert.Equal(t, "restaurant-1", connection.GetEdges()[0].GetCursor())
	assert.Equal(t, "Pho Bar", connection.GetEdges()[0].GetNode().GetName())
	assert.True(t, connection.GetEdges()[0].GetNode().GetAcceptingOrders())
	assert.Nil(t, connection.GetEdges()[0].GetNode().GetMenu(), "menu is not filled in the list")
	assert.True(t, connection.GetPageInfo().GetHasNextPage())
	assert.Equal(t, "restaurant-1", connection.GetPageInfo().GetEndCursor().GetValue())
}

func TestQueryRestaurantsEmptyPageHasNoEndCursor(t *testing.T) {
	response, err := catalogService(&fakeListRestaurants{}, &fakeGetRestaurant{}).QueryRestaurants(context.Background(),
		&service.QueryRestaurantsRequest{First: 10})
	require.NoError(t, err)
	assert.Empty(t, response.GetRestaurants().GetEdges())
	assert.Nil(t, response.GetRestaurants().GetPageInfo().GetEndCursor())
}

func TestQueryRestaurantsErrors(t *testing.T) {
	_, err := catalogService(&fakeListRestaurants{err: applications.ErrInvalidPageSize}, &fakeGetRestaurant{}).
		QueryRestaurants(context.Background(), &service.QueryRestaurantsRequest{First: 0})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = catalogService(&fakeListRestaurants{err: errors.New("down")}, &fakeGetRestaurant{}).
		QueryRestaurants(context.Background(), &service.QueryRestaurantsRequest{First: 10})
	assert.Equal(t, codes.Unavailable, status.Code(err))

	_, err = (&BffService{}).QueryRestaurants(context.Background(), &service.QueryRestaurantsRequest{First: 10})
	assert.Equal(t, codes.Unavailable, status.Code(err), "catalog is not connected")
}

// AC-568: a restaurant comes with its menu, prices and availability.
func TestQueryRestaurantMapsTheMenu(t *testing.T) {
	get := &fakeGetRestaurant{view: applications.RestaurantView{Restaurant: phoBar, Menu: []applications.MenuItem{
		{ID: "menu-gyoza", Name: "Гёдза", Description: "6 шт.", Price: applications.Money{Amount: "4.00", Currency: "EUR"}},
		{ID: "menu-pho", Name: "Фо", Price: applications.Money{Amount: "9.50", Currency: "EUR"}, Available: true},
	}}}

	response, err := catalogService(&fakeListRestaurants{}, get).QueryRestaurant(context.Background(), &service.QueryRestaurantRequest{Id: "restaurant-1"})

	require.NoError(t, err)
	restaurant := response.GetRestaurant()
	assert.Equal(t, "ул. Садовая, 8", restaurant.GetAddress())
	items := restaurant.GetMenu().GetItems()
	require.Len(t, items, 2)
	assert.Equal(t, "6 шт.", items[0].GetDescription().GetValue())
	assert.False(t, items[0].GetAvailable())
	assert.Equal(t, "4.00", items[0].GetPrice().GetAmount())
	assert.Nil(t, items[1].GetDescription(), "no description is null")
	assert.True(t, items[1].GetAvailable())
}

func TestQueryRestaurantMissingIsNullAndWithoutMenuHasNullMenu(t *testing.T) {
	response, err := catalogService(&fakeListRestaurants{}, &fakeGetRestaurant{err: applications.ErrRestaurantNotFound}).
		QueryRestaurant(context.Background(), &service.QueryRestaurantRequest{Id: "missing"})
	require.NoError(t, err)
	assert.Nil(t, response.GetRestaurant())

	response, err = catalogService(&fakeListRestaurants{}, &fakeGetRestaurant{view: applications.RestaurantView{Restaurant: phoBar}}).
		QueryRestaurant(context.Background(), &service.QueryRestaurantRequest{Id: "restaurant-1"})
	require.NoError(t, err)
	assert.NotNil(t, response.GetRestaurant())
	assert.Nil(t, response.GetRestaurant().GetMenu())
}
