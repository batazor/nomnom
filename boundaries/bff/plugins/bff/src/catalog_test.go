package main

import (
	"context"
	"errors"
	"testing"
	"time"

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
	ID: "restaurant-1", Name: "Pho Bar", AcceptingOrders: true,
	WaitTime:     applications.WaitTime{Elevated: true, ExpectedWaitMinutes: 42},
	TimeZone:     "Europe/Berlin",
	OpeningHours: []applications.OpeningInterval{{Day: 5, Opens: "18:00", Closes: "02:00"}},
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

// AC-949: a restaurant opened by its owner before its opening hours does not
// accept orders and names the start of the next period; its schedule and time
// zone are public.
func TestQueryRestaurantMapsTheOpeningHours(t *testing.T) {
	opensAt := time.Date(2026, 10, 30, 17, 0, 0, 0, time.UTC)
	closed := phoBar
	closed.AcceptingOrders = false
	closed.NextOpeningAt = &opensAt
	get := newMockGetRestaurantHandler(t)
	get.EXPECT().Handle(mock.Anything, "restaurant-1").Return(applications.RestaurantView{Restaurant: closed}, nil).Once()

	response, err := catalogService(newMockListRestaurantsHandler(t), get).QueryRestaurant(context.Background(), &service.QueryRestaurantRequest{Id: "restaurant-1"})

	require.NoError(t, err)
	restaurant := response.GetRestaurant()
	assert.False(t, restaurant.GetAcceptingOrders())
	assert.Equal(t, "2026-10-30T17:00:00Z", restaurant.GetNextOpeningAt().GetValue())
	assert.Equal(t, "Europe/Berlin", restaurant.GetTimeZone())
	assert.Equal(t, []*service.OpeningInterval{{DayOfWeek: 5, Opens: "18:00", Closes: "02:00"}}, restaurant.GetOpeningHours())

	get.EXPECT().Handle(mock.Anything, "restaurant-1").Return(applications.RestaurantView{Restaurant: phoBar}, nil).Once()
	response, err = catalogService(newMockListRestaurantsHandler(t), get).QueryRestaurant(context.Background(), &service.QueryRestaurantRequest{Id: "restaurant-1"})
	require.NoError(t, err)
	assert.Nil(t, response.GetRestaurant().GetNextOpeningAt(), "an accepting restaurant has no next opening")
}

// FR-685: the windows of each restaurant of the response, in its order.
func TestResolveScheduledSlotsKeepsTheOrderOfTheContext(t *testing.T) {
	start := time.Date(2026, 10, 28, 17, 0, 0, 0, time.UTC)
	slots := newMockListScheduledSlotsHandler(t)
	slots.EXPECT().Handle(mock.Anything, []string{"restaurant-1", "restaurant-2"}).Return([][]applications.Slot{
		{{Start: start, End: start.Add(15 * time.Minute), Available: true}, {Start: start.Add(15 * time.Minute), End: start.Add(30 * time.Minute)}},
		{},
	}, nil).Once()
	svc := &BffService{catalog: &restaurantCatalog{listScheduledSlots: slots}}

	response, err := svc.ResolveRestaurantScheduledSlots(context.Background(), &service.ResolveRestaurantScheduledSlotsRequest{
		Context: []*service.ResolveRestaurantScheduledSlotsContext{{Id: "restaurant-1"}, {Id: "restaurant-2"}},
	})

	require.NoError(t, err)
	require.Len(t, response.GetResult(), 2)
	assert.Equal(t, []*service.DeliveryWindow{
		{Start: "2026-10-28T17:00:00Z", End: "2026-10-28T17:15:00Z", Available: true},
		{Start: "2026-10-28T17:15:00Z", End: "2026-10-28T17:30:00Z"},
	}, response.GetResult()[0].GetScheduledSlots())
	assert.Empty(t, response.GetResult()[1].GetScheduledSlots())

	broken := newMockListScheduledSlotsHandler(t)
	broken.EXPECT().Handle(mock.Anything, []string{"restaurant-1"}).Return(nil, errors.New("down")).Once()
	svc.catalog.listScheduledSlots = broken
	_, err = svc.ResolveRestaurantScheduledSlots(context.Background(), &service.ResolveRestaurantScheduledSlotsRequest{
		Context: []*service.ResolveRestaurantScheduledSlotsContext{{Id: "restaurant-1"}},
	})
	assert.Equal(t, codes.Unavailable, status.Code(err))
}
