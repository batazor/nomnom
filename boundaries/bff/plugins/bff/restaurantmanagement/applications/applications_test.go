package applications

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeOpener struct {
	result OpeningResult
	err    error
	calls  []string
}

func (f *fakeOpener) Open(_ context.Context, restaurantID string) (OpeningResult, error) {
	f.calls = append(f.calls, "open "+restaurantID)
	return f.result, f.err
}

func (f *fakeOpener) Close(_ context.Context, restaurantID string) (OpeningResult, error) {
	f.calls = append(f.calls, "close "+restaurantID)
	return f.result, f.err
}

var bakery = Restaurant{ID: "restaurant-1", Name: "Ночная пекарня", IsOpen: true}

func TestOpenAndCloseCallTheirCommands(t *testing.T) {
	opener := &fakeOpener{result: OpeningResult{Restaurant: bakery, Changed: true}}

	opened, err := NewOpenRestaurant(opener).Handle(context.Background(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, OpeningResult{Restaurant: bakery, Changed: true}, opened)

	_, err = NewCloseRestaurant(opener).Handle(context.Background(), "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, []string{"open restaurant-1", "close restaurant-1"}, opener.calls)
}

func TestOpeningPassesRefusalsThrough(t *testing.T) {
	for _, refusal := range []error{ErrRestaurantNotFound, ErrNotPermitted, ErrInvalidRestaurant} {
		_, err := NewOpenRestaurant(&fakeOpener{err: refusal}).Handle(context.Background(), "restaurant-1")
		assert.ErrorIs(t, err, refusal)
		_, err = NewCloseRestaurant(&fakeOpener{err: refusal}).Handle(context.Background(), "restaurant-1")
		assert.ErrorIs(t, err, refusal)
	}
}

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
