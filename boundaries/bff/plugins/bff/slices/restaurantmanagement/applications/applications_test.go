package applications

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	catalog "github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/catalog/applications"
)

var bakery = Restaurant{ID: "restaurant-1", Name: "Ночная пекарня", IsOpen: true}

func TestListOwnedRestaurantsPagesWithTheCursorOfRestaurants(t *testing.T) {
	closed := Restaurant{ID: "restaurant-2", Name: "Пельменная"}
	lister := NewMockOwnedRestaurantLister(t)
	lister.EXPECT().ListOwned(mock.Anything, 2, "restaurant-0").
		Return(OwnedPage{Restaurants: []Restaurant{bakery, closed}, NextCursor: "restaurant-2"}, nil).Once()

	page, err := NewListOwnedRestaurants(lister).Handle(context.Background(), 2, "restaurant-0")

	require.NoError(t, err)
	assert.Equal(t, RestaurantPage{
		Edges:       []RestaurantEdge{{Cursor: "restaurant-1", Restaurant: bakery}, {Cursor: "restaurant-2", Restaurant: closed}},
		HasNextPage: true,
		EndCursor:   "restaurant-2",
	}, page)
}

func TestListOwnedRestaurantsRejectsAPageSizeOutOfRange(t *testing.T) {
	// restaurants is not called: the lister expects no call.
	lister := NewMockOwnedRestaurantLister(t)
	for _, first := range []int{0, MaxPageSize + 1} {
		_, err := NewListOwnedRestaurants(lister).Handle(context.Background(), first, "")
		assert.ErrorIs(t, err, ErrInvalidPageSize)
	}
}

// AC-951: the whole schedule goes to restaurants, the restaurant is read back.
func TestSetOpeningHoursReadsTheRestaurantBack(t *testing.T) {
	hours := []catalog.OpeningInterval{{Day: 5, Opens: "18:00", Closes: "02:00"}}
	view := catalog.RestaurantView{Restaurant: catalog.Restaurant{ID: "restaurant-1", OpeningHours: hours}}
	setter := NewMockOpeningHoursSetter(t)
	setter.EXPECT().SetOpeningHours(mock.Anything, "restaurant-1", hours).Return(true, nil).Once()
	views := NewMockRestaurantViews(t)
	views.EXPECT().Handle(mock.Anything, "restaurant-1").Return(view, nil).Once()

	result, err := NewSetOpeningHours(setter, views).Handle(context.Background(), "restaurant-1", hours)

	require.NoError(t, err)
	assert.Equal(t, OpeningHoursResult{Restaurant: view, Changed: true}, result)
}

func TestSetOpeningHoursRefusalReadsNothing(t *testing.T) {
	overlap := InvalidOpeningHours{Reason: "overlap", Day: 5, Opens: "18:00", Closes: "02:00"}
	setter := NewMockOpeningHoursSetter(t)
	setter.EXPECT().SetOpeningHours(mock.Anything, "restaurant-1", []catalog.OpeningInterval{}).Return(false, overlap).Once()

	// No restaurant to read back: views expects no call.
	_, err := NewSetOpeningHours(setter, NewMockRestaurantViews(t)).Handle(context.Background(), "restaurant-1", []catalog.OpeningInterval{})

	assert.ErrorIs(t, err, overlap)
}
