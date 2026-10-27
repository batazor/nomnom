package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

var phoBar = applications.Restaurant{
	ID: "restaurant-1", Name: "Pho Bar", IsOpen: true,
	WaitTime: applications.WaitTime{Elevated: true, ExpectedWaitMinutes: 42},
}

// Catalog resolvers need no BFF identity in the service and no access token.
func catalogService(list *mockListRestaurantsHandler, get *mockGetRestaurantHandler) *BffService {
	return &BffService{catalog: &restaurantCatalog{listRestaurants: list, getRestaurant: get}}
}

func TestQueryRestaurantsMapsThePageWithoutAccessToken(t *testing.T) {
	list := newMockListRestaurantsHandler(t)
	list.EXPECT().Handle(mock.Anything, 1, "restaurant-0").Return(applications.RestaurantPage{
		Edges:       []applications.RestaurantEdge{{Cursor: "restaurant-1", Restaurant: phoBar}},
		HasNextPage: true,
		EndCursor:   "restaurant-1",
	}, nil).Once()

	response, err := catalogService(list, newMockGetRestaurantHandler(t)).QueryRestaurants(context.Background(),
		&service.QueryRestaurantsRequest{First: 1, After: wrapperspb.String("restaurant-0")})

	require.NoError(t, err)
	connection := response.GetRestaurants()
	require.Len(t, connection.GetEdges(), 1)
	assert.Equal(t, "restaurant-1", connection.GetEdges()[0].GetCursor())
	assert.Equal(t, "Pho Bar", connection.GetEdges()[0].GetNode().GetName())
	assert.True(t, connection.GetEdges()[0].GetNode().GetAcceptingOrders())
	assert.True(t, connection.GetEdges()[0].GetNode().GetWaitTime().GetElevated(), "FR-647")
	assert.Equal(t, int32(42), connection.GetEdges()[0].GetNode().GetWaitTime().GetExpectedWaitMinutes())
	assert.Nil(t, connection.GetEdges()[0].GetNode().GetMenu(), "menu is not filled in the list")
	assert.True(t, connection.GetPageInfo().GetHasNextPage())
	assert.Equal(t, "restaurant-1", connection.GetPageInfo().GetEndCursor().GetValue())
}

func TestQueryRestaurantsEmptyPageHasNoEndCursor(t *testing.T) {
	list := newMockListRestaurantsHandler(t)
	list.EXPECT().Handle(mock.Anything, 10, "").Return(applications.RestaurantPage{}, nil).Once()
	response, err := catalogService(list, newMockGetRestaurantHandler(t)).QueryRestaurants(context.Background(),
		&service.QueryRestaurantsRequest{First: 10})
	require.NoError(t, err)
	assert.Empty(t, response.GetRestaurants().GetEdges())
	assert.Nil(t, response.GetRestaurants().GetPageInfo().GetEndCursor())
}

func TestQueryRestaurantsErrors(t *testing.T) {
	list := newMockListRestaurantsHandler(t)
	list.EXPECT().Handle(mock.Anything, 0, "").Return(applications.RestaurantPage{}, applications.ErrInvalidPageSize).Once()
	_, err := catalogService(list, newMockGetRestaurantHandler(t)).
		QueryRestaurants(context.Background(), &service.QueryRestaurantsRequest{First: 0})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	list = newMockListRestaurantsHandler(t)
	list.EXPECT().Handle(mock.Anything, 10, "").Return(applications.RestaurantPage{}, errors.New("down")).Once()
	_, err = catalogService(list, newMockGetRestaurantHandler(t)).
		QueryRestaurants(context.Background(), &service.QueryRestaurantsRequest{First: 10})
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// AC-568: a restaurant comes with its menu, prices and availability.
func TestQueryRestaurantMapsTheMenu(t *testing.T) {
	get := newMockGetRestaurantHandler(t)
	get.EXPECT().Handle(mock.Anything, "restaurant-1").Return(applications.RestaurantView{Restaurant: phoBar, Menu: []applications.MenuItem{
		{ID: "menu-gyoza", Name: "Гёдза", Description: "6 шт.", Price: applications.Money{Amount: "4.00", Currency: "EUR"}},
		{ID: "menu-pho", Name: "Фо", Price: applications.Money{Amount: "9.50", Currency: "EUR"}, Available: true},
	}}, nil).Once()

	response, err := catalogService(newMockListRestaurantsHandler(t), get).QueryRestaurant(context.Background(), &service.QueryRestaurantRequest{Id: "restaurant-1"})

	require.NoError(t, err)
	restaurant := response.GetRestaurant()
	items := restaurant.GetMenu().GetItems()
	require.Len(t, items, 2)
	assert.Equal(t, "6 шт.", items[0].GetDescription().GetValue())
	assert.False(t, items[0].GetAvailable())
	assert.Equal(t, "4.00", items[0].GetPrice().GetAmount())
	assert.Nil(t, items[1].GetDescription(), "no description is null")
	assert.True(t, items[1].GetAvailable())
}

func TestQueryRestaurantMissingIsNullAndWithoutMenuHasNullMenu(t *testing.T) {
	get := newMockGetRestaurantHandler(t)
	get.EXPECT().Handle(mock.Anything, "missing").Return(applications.RestaurantView{}, applications.ErrRestaurantNotFound).Once()
	get.EXPECT().Handle(mock.Anything, "restaurant-1").Return(applications.RestaurantView{Restaurant: phoBar}, nil).Once()
	response, err := catalogService(newMockListRestaurantsHandler(t), get).
		QueryRestaurant(context.Background(), &service.QueryRestaurantRequest{Id: "missing"})
	require.NoError(t, err)
	assert.Nil(t, response.GetRestaurant())

	response, err = catalogService(newMockListRestaurantsHandler(t), get).
		QueryRestaurant(context.Background(), &service.QueryRestaurantRequest{Id: "restaurant-1"})
	require.NoError(t, err)
	assert.NotNil(t, response.GetRestaurant())
	assert.Nil(t, response.GetRestaurant().GetMenu())
}
