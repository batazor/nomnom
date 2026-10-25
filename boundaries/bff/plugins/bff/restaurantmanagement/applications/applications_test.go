package applications

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var bakery = Restaurant{ID: "restaurant-1", Name: "Ночная пекарня", IsOpen: true}

type fakeOwnedLister struct {
	page   OwnedPage
	size   int
	cursor string
}

func (f *fakeOwnedLister) ListOwned(_ context.Context, size int, cursor string) (OwnedPage, error) {
	f.size, f.cursor = size, cursor
	return f.page, nil
}

func TestListOwnedRestaurantsPagesWithTheCursorOfRestaurants(t *testing.T) {
	closed := Restaurant{ID: "restaurant-2", Name: "Пельменная"}
	lister := &fakeOwnedLister{page: OwnedPage{Restaurants: []Restaurant{bakery, closed}, NextCursor: "restaurant-2"}}

	page, err := NewListOwnedRestaurants(lister).Handle(context.Background(), 2, "restaurant-0")

	require.NoError(t, err)
	assert.Equal(t, 2, lister.size)
	assert.Equal(t, "restaurant-0", lister.cursor)
	assert.Equal(t, RestaurantPage{
		Edges:       []RestaurantEdge{{Cursor: "restaurant-1", Restaurant: bakery}, {Cursor: "restaurant-2", Restaurant: closed}},
		HasNextPage: true,
		EndCursor:   "restaurant-2",
	}, page)
}

func TestListOwnedRestaurantsRejectsAPageSizeOutOfRange(t *testing.T) {
	lister := &fakeOwnedLister{}
	for _, first := range []int{0, MaxPageSize + 1} {
		_, err := NewListOwnedRestaurants(lister).Handle(context.Background(), first, "")
		assert.ErrorIs(t, err, ErrInvalidPageSize)
	}
	assert.Zero(t, lister.size, "restaurants is not called")
}
