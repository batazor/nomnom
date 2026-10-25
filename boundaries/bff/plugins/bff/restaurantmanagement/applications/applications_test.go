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
