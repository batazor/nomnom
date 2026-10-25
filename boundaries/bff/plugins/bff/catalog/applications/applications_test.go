package applications

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRestaurants struct {
	page       Page
	got        PageRequest
	restaurant Restaurant
	err        error
	menu       []MenuItem
	menuErr    error
}

func (f *fakeRestaurants) List(_ context.Context, request PageRequest) (Page, error) {
	f.got = request
	return f.page, f.err
}

func (f *fakeRestaurants) Restaurant(context.Context, string) (Restaurant, error) {
	return f.restaurant, f.err
}

func (f *fakeRestaurants) Menu(context.Context, string) ([]MenuItem, error) {
	return f.menu, f.menuErr
}

var (
	pho   = Restaurant{ID: "restaurant-1", Name: "Pho Bar", Address: "ул. Садовая, 8", IsOpen: true}
	ramen = Restaurant{ID: "restaurant-2", Name: "Ramen Bar", IsOpen: true}
)

func TestListRestaurantsPassesTheCursorThrough(t *testing.T) {
	restaurants := &fakeRestaurants{page: Page{Restaurants: []Restaurant{pho}, NextCursor: "restaurant-1"}}

	page, err := NewListRestaurants(restaurants, restaurants).Handle(context.Background(), 1, "restaurant-0")

	require.NoError(t, err)
	assert.Equal(t, PageRequest{Size: 1, Cursor: "restaurant-0"}, restaurants.got)
	assert.Equal(t, RestaurantPage{
		Edges:       []RestaurantEdge{{Cursor: "restaurant-1", Restaurant: pho, Menu: []MenuItem{}}},
		HasNextPage: true,
		EndCursor:   "restaurant-1",
	}, page)
}

func TestListRestaurantsLastPageEndsAtItsLastEdge(t *testing.T) {
	restaurants := &fakeRestaurants{page: Page{Restaurants: []Restaurant{pho, ramen}}}

	page, err := NewListRestaurants(restaurants, restaurants).Handle(context.Background(), 10, "")

	require.NoError(t, err)
	assert.False(t, page.HasNextPage)
	assert.Equal(t, "restaurant-2", page.EndCursor)

	restaurants.page = Page{}
	page, err = NewListRestaurants(restaurants, restaurants).Handle(context.Background(), 10, "")
	require.NoError(t, err)
	assert.Empty(t, page.Edges)
	assert.Empty(t, page.EndCursor)
}

func TestListRestaurantsRejectsAnUnusablePageSize(t *testing.T) {
	for _, first := range []int{0, -1, MaxPageSize + 1} {
		_, err := NewListRestaurants(&fakeRestaurants{}, &fakeRestaurants{}).Handle(context.Background(), first, "")
		assert.ErrorIs(t, err, ErrInvalidPageSize, first)
	}
}

// AC-568: a restaurant comes with its menu, prices and availability.
func TestGetRestaurantReturnsTheMenu(t *testing.T) {
	gyoza := MenuItem{ID: "menu-gyoza", Name: "Гёдза", Price: Money{Amount: "4.00", Currency: "EUR"}}
	restaurants := &fakeRestaurants{restaurant: pho, menu: []MenuItem{gyoza}}

	view, err := NewGetRestaurant(restaurants).Handle(context.Background(), "restaurant-1")

	require.NoError(t, err)
	assert.Equal(t, RestaurantView{Restaurant: pho, Menu: []MenuItem{gyoza}}, view)
}

func TestGetRestaurantWithoutMenuHasNoMenu(t *testing.T) {
	view, err := NewGetRestaurant(&fakeRestaurants{restaurant: pho, menuErr: ErrMenuNotFound}).Handle(context.Background(), "restaurant-1")
	require.NoError(t, err)
	assert.Nil(t, view.Menu)

	view, err = NewGetRestaurant(&fakeRestaurants{restaurant: pho}).Handle(context.Background(), "restaurant-1")
	require.NoError(t, err)
	assert.NotNil(t, view.Menu, "an empty published menu is not a missing one")
}

func TestGetRestaurantErrors(t *testing.T) {
	_, err := NewGetRestaurant(&fakeRestaurants{err: ErrRestaurantNotFound}).Handle(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrRestaurantNotFound)

	down := errors.New("unavailable")
	_, err = NewGetRestaurant(&fakeRestaurants{restaurant: pho, menuErr: down}).Handle(context.Background(), "restaurant-1")
	assert.ErrorIs(t, err, down)
}

func TestListRestaurantsCarriesEachMenu(t *testing.T) {
	soup := MenuItem{ID: "menu-1", Name: "Фо бо", Price: Money{Amount: "450", Currency: "RUB"}, Available: true}
	restaurants := &fakeRestaurants{page: Page{Restaurants: []Restaurant{pho}}}
	menus := menusByRestaurant{pho.ID: {soup}}

	page, err := NewListRestaurants(restaurants, menus).Handle(context.Background(), 10, "")

	require.NoError(t, err)
	assert.Equal(t, []MenuItem{soup}, page.Edges[0].Menu)
}

func TestListRestaurantsLeavesAMissingMenuNil(t *testing.T) {
	restaurants := &fakeRestaurants{page: Page{Restaurants: []Restaurant{pho}}, menuErr: ErrMenuNotFound}

	page, err := NewListRestaurants(restaurants, restaurants).Handle(context.Background(), 10, "")

	require.NoError(t, err)
	assert.Nil(t, page.Edges[0].Menu)
}

func TestListRestaurantsFailsWhenAMenuCannotBeRead(t *testing.T) {
	broken := errors.New("restaurants unavailable")
	restaurants := &fakeRestaurants{page: Page{Restaurants: []Restaurant{pho, ramen}}, menuErr: broken}

	_, err := NewListRestaurants(restaurants, restaurants).Handle(context.Background(), 10, "")

	require.ErrorIs(t, err, broken)
}

type menusByRestaurant map[string][]MenuItem

func (m menusByRestaurant) Menu(_ context.Context, restaurantID string) ([]MenuItem, error) {
	return m[restaurantID], nil
}
