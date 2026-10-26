package applications

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHistory struct {
	page HistoryPage
	err  error
	got  []any
}

func (f *fakeHistory) ListCustomerOrders(_ context.Context, customerID string, size int, cursor string) (HistoryPage, error) {
	f.got = []any{customerID, size, cursor}
	return f.page, f.err
}

func entry(id, restaurantID, cursor string) HistoryEntry {
	order := snapshot("placed")
	order.ID, order.RestaurantID = id, restaurantID
	return HistoryEntry{Order: order, Cursor: cursor}
}

func list(history OrderHistory, catalog Catalog) ListCustomerOrders {
	h := NewListCustomerOrders(history, catalog)
	h.now = func() time.Time { return at(1) }
	return h
}

// The history carries the full card of each Order: one restaurants read per
// distinct restaurant of the page and no GetOrder per Order.
func TestCustomerOrdersAreFullOrderViewsWithTheirCursors(t *testing.T) {
	history := &fakeHistory{page: HistoryPage{
		Entries:    []HistoryEntry{entry("order-3", "restaurant-1", "c3"), entry("order-2", "restaurant-2", "c2"), entry("order-1", "restaurant-1", "c1")},
		NextCursor: "c1",
	}}
	reads := map[string]int{}

	page, err := list(history, fakeCatalog{reads: reads}).Handle(context.Background(), "customer-1", 3, "c4")

	require.NoError(t, err)
	assert.Equal(t, []any{"customer-1", 3, "c4"}, history.got)
	require.Len(t, page.Edges, 3)
	assert.Equal(t, []string{"c3", "c2", "c1"}, []string{page.Edges[0].Cursor, page.Edges[1].Cursor, page.Edges[2].Cursor})
	assert.Equal(t, "order-2", page.Edges[1].Order.ID)
	assert.Equal(t, "restaurant-2", page.Edges[1].Order.Restaurant.ID)
	assert.Equal(t, "Чай", page.Edges[0].Order.Items[0].Name)
	assert.Equal(t, StatusReceived, page.Edges[0].Order.Status)
	assert.True(t, page.HasNextPage)
	assert.Equal(t, "c1", page.EndCursor)
	assert.Equal(t, map[string]int{"restaurant-1": 1, "restaurant-2": 1}, reads)
}

func TestLastHistoryPage(t *testing.T) {
	history := &fakeHistory{page: HistoryPage{Entries: []HistoryEntry{entry("order-1", "restaurant-1", "c1")}}}
	page, err := list(history, fakeCatalog{}).Handle(context.Background(), "customer-1", 10, "")
	require.NoError(t, err)
	assert.False(t, page.HasNextPage)
	assert.Equal(t, "c1", page.EndCursor)

	empty, err := list(&fakeHistory{}, fakeCatalog{}).Handle(context.Background(), "customer-1", 10, "")
	require.NoError(t, err)
	assert.Equal(t, OrderPage{Edges: []OrderEdge{}}, empty)
}

func TestCustomerOrdersRefusals(t *testing.T) {
	h := list(&fakeHistory{}, fakeCatalog{})
	for _, first := range []int{0, 101} {
		_, err := h.Handle(context.Background(), "customer-1", first, "")
		assert.ErrorIs(t, err, ErrInvalidPageSize)
	}
	_, err := list(&fakeHistory{err: ErrInvalidCursor}, fakeCatalog{}).Handle(context.Background(), "customer-1", 1, "x")
	assert.ErrorIs(t, err, ErrInvalidCursor)

	down := errors.New("restaurants is down")
	history := &fakeHistory{page: HistoryPage{Entries: []HistoryEntry{entry("order-1", "restaurant-1", "c1")}}}
	_, err = list(history, fakeCatalog{err: map[string]error{"restaurant-1": down}}).Handle(context.Background(), "customer-1", 1, "")
	assert.ErrorIs(t, err, down)

	broken := entry("order-1", "restaurant-1", "c1")
	broken.Order.State = "lost"
	_, err = list(&fakeHistory{page: HistoryPage{Entries: []HistoryEntry{broken}}}, fakeCatalog{}).Handle(context.Background(), "customer-1", 1, "")
	assert.Error(t, err, "an unknown state fails the page instead of being guessed")
}
