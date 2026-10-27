package applications

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
