package applications

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	pho   = Restaurant{ID: "restaurant-1", Name: "Pho Bar", IsOpen: true}
	ramen = Restaurant{ID: "restaurant-2", Name: "Ramen Bar", IsOpen: true}
)

func TestListRestaurantsPassesTheCursorThrough(t *testing.T) {
	restaurants := NewMockRestaurantLister(t)
	restaurants.EXPECT().List(mock.Anything, PageRequest{Size: 1, Cursor: "restaurant-0"}).
		Return(Page{Restaurants: []Restaurant{pho}, NextCursor: "restaurant-1"}, nil).Once()
	menus := NewMockMenuBatchReader(t)
	menus.EXPECT().Menus(mock.Anything, []string{pho.ID}).Return(map[string][]MenuItem{pho.ID: {}}, nil).Once()

	page, err := NewListRestaurants(restaurants, menus).Handle(context.Background(), 1, "restaurant-0")

	require.NoError(t, err)
	assert.Equal(t, RestaurantPage{
		Edges:       []RestaurantEdge{{Cursor: "restaurant-1", Restaurant: pho, Menu: []MenuItem{}}},
		HasNextPage: true,
		EndCursor:   "restaurant-1",
	}, page)
}

func TestListRestaurantsLastPageEndsAtItsLastEdge(t *testing.T) {
	restaurants := NewMockRestaurantLister(t)
	restaurants.EXPECT().List(mock.Anything, PageRequest{Size: 10}).Return(Page{Restaurants: []Restaurant{pho, ramen}}, nil).Once()
	restaurants.EXPECT().List(mock.Anything, PageRequest{Size: 10}).Return(Page{}, nil).Once()
	menus := NewMockMenuBatchReader(t)
	menus.EXPECT().Menus(mock.Anything, []string{pho.ID, ramen.ID}).Return(map[string][]MenuItem{}, nil).Once()

	page, err := NewListRestaurants(restaurants, menus).Handle(context.Background(), 10, "")

	require.NoError(t, err)
	assert.False(t, page.HasNextPage)
	assert.Equal(t, "restaurant-2", page.EndCursor)

	page, err = NewListRestaurants(restaurants, menus).Handle(context.Background(), 10, "")
	require.NoError(t, err)
	assert.Empty(t, page.Edges)
	assert.Empty(t, page.EndCursor)
}

func TestListRestaurantsRejectsAnUnusablePageSize(t *testing.T) {
	for _, first := range []int{0, -1, MaxPageSize + 1} {
		_, err := NewListRestaurants(NewMockRestaurantLister(t), NewMockMenuBatchReader(t)).Handle(context.Background(), first, "")
		assert.ErrorIs(t, err, ErrInvalidPageSize, first)
	}
}

// AC-568: a restaurant comes with its menu, prices and availability.
func TestGetRestaurantReturnsTheMenu(t *testing.T) {
	gyoza := MenuItem{ID: "menu-gyoza", Name: "Гёдза", Price: Money{Amount: "4.00", Currency: "EUR"}}
	restaurants := NewMockRestaurantReader(t)
	restaurants.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(pho, nil).Once()
	restaurants.EXPECT().Menu(mock.Anything, "restaurant-1").Return([]MenuItem{gyoza}, nil).Once()

	view, err := NewGetRestaurant(restaurants).Handle(context.Background(), "restaurant-1")

	require.NoError(t, err)
	assert.Equal(t, RestaurantView{Restaurant: pho, Menu: []MenuItem{gyoza}}, view)
}

func TestGetRestaurantWithoutMenuHasNoMenu(t *testing.T) {
	unpublished := NewMockRestaurantReader(t)
	unpublished.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(pho, nil).Once()
	unpublished.EXPECT().Menu(mock.Anything, "restaurant-1").Return(nil, ErrMenuNotFound).Once()
	view, err := NewGetRestaurant(unpublished).Handle(context.Background(), "restaurant-1")
	require.NoError(t, err)
	assert.Nil(t, view.Menu)

	empty := NewMockRestaurantReader(t)
	empty.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(pho, nil).Once()
	empty.EXPECT().Menu(mock.Anything, "restaurant-1").Return(nil, nil).Once()
	view, err = NewGetRestaurant(empty).Handle(context.Background(), "restaurant-1")
	require.NoError(t, err)
	assert.NotNil(t, view.Menu, "an empty published menu is not a missing one")
}

func TestGetRestaurantErrors(t *testing.T) {
	missing := NewMockRestaurantReader(t)
	missing.EXPECT().Restaurant(mock.Anything, "missing").Return(Restaurant{}, ErrRestaurantNotFound).Once()
	_, err := NewGetRestaurant(missing).Handle(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrRestaurantNotFound)

	down := errors.New("unavailable")
	broken := NewMockRestaurantReader(t)
	broken.EXPECT().Restaurant(mock.Anything, "restaurant-1").Return(pho, nil).Once()
	broken.EXPECT().Menu(mock.Anything, "restaurant-1").Return(nil, down).Once()
	_, err = NewGetRestaurant(broken).Handle(context.Background(), "restaurant-1")
	assert.ErrorIs(t, err, down)
}

func TestListRestaurantsReadsTheMenusOfThePageInOneCall(t *testing.T) {
	soup := MenuItem{ID: "menu-1", Name: "Фо бо", Price: Money{Amount: "450", Currency: "RUB"}, Available: true}
	restaurants := NewMockRestaurantLister(t)
	restaurants.EXPECT().List(mock.Anything, PageRequest{Size: 10}).Return(Page{Restaurants: []Restaurant{pho}}, nil).Once()
	menus := NewMockMenuBatchReader(t)
	menus.EXPECT().Menus(mock.Anything, []string{pho.ID}).Return(map[string][]MenuItem{pho.ID: {soup}}, nil).Once()

	page, err := NewListRestaurants(restaurants, menus).Handle(context.Background(), 10, "")

	require.NoError(t, err)
	assert.Equal(t, []MenuItem{soup}, page.Edges[0].Menu)
}

func TestListRestaurantsLeavesAMissingMenuNil(t *testing.T) {
	restaurants := NewMockRestaurantLister(t)
	restaurants.EXPECT().List(mock.Anything, PageRequest{Size: 10}).Return(Page{Restaurants: []Restaurant{pho}}, nil).Once()
	menus := NewMockMenuBatchReader(t)
	menus.EXPECT().Menus(mock.Anything, []string{pho.ID}).Return(map[string][]MenuItem{}, nil).Once()

	page, err := NewListRestaurants(restaurants, menus).Handle(context.Background(), 10, "")

	require.NoError(t, err)
	assert.Nil(t, page.Edges[0].Menu)
}

func TestListRestaurantsFailsWhenAMenuCannotBeRead(t *testing.T) {
	broken := errors.New("restaurants unavailable")
	restaurants := NewMockRestaurantLister(t)
	restaurants.EXPECT().List(mock.Anything, PageRequest{Size: 10}).Return(Page{Restaurants: []Restaurant{pho, ramen}}, nil).Once()
	menus := NewMockMenuBatchReader(t)
	menus.EXPECT().Menus(mock.Anything, []string{pho.ID, ramen.ID}).Return(nil, broken).Once()

	_, err := NewListRestaurants(restaurants, menus).Handle(context.Background(), 10, "")

	require.ErrorIs(t, err, broken)
}
