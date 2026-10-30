package applications

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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

// The history carries the full card of each Order: one restaurants read for
// the distinct restaurants of the page (ADR 7) and no GetOrder per Order.
func TestCustomerOrdersAreFullOrderViewsWithTheirCursors(t *testing.T) {
	history := NewMockOrderHistory(t)
	history.EXPECT().ListCustomerOrders(mock.Anything, "customer-1", 3, "c4").Return(HistoryPage{
		Entries:    []HistoryEntry{entry("order-3", "restaurant-1", "c3"), entry("order-2", "restaurant-2", "c2"), entry("order-1", "restaurant-1", "c1")},
		NextCursor: "c1",
	}, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurants(mock.Anything, []string{"restaurant-1", "restaurant-2"}).Return(map[string]Restaurant{
		"restaurant-1": {ID: "restaurant-1", Name: "Кухня №8", IsOpen: true},
		"restaurant-2": {ID: "restaurant-2", Name: "Кухня №8", IsOpen: true},
	}, nil).Once()

	page, err := list(history, catalog).Handle(context.Background(), "customer-1", 3, "c4")

	require.NoError(t, err)
	require.Len(t, page.Edges, 3)
	assert.Equal(t, []string{"c3", "c2", "c1"}, []string{page.Edges[0].Cursor, page.Edges[1].Cursor, page.Edges[2].Cursor})
	assert.Equal(t, "order-2", page.Edges[1].Order.ID)
	assert.Equal(t, "restaurant-2", page.Edges[1].Order.Restaurant.ID)
	assert.Equal(t, "Чай", page.Edges[0].Order.Items[0].Name)
	assert.Equal(t, StatusReceived, page.Edges[0].Order.Status)
	assert.True(t, page.HasNextPage)
	assert.Equal(t, "c1", page.EndCursor)
}

func TestLastHistoryPage(t *testing.T) {
	history := NewMockOrderHistory(t)
	history.EXPECT().ListCustomerOrders(mock.Anything, "customer-1", 10, "").Return(HistoryPage{Entries: []HistoryEntry{entry("order-1", "restaurant-1", "c1")}}, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurants(mock.Anything, []string{"restaurant-1"}).Return(map[string]Restaurant{"restaurant-1": Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}}, nil).Once()
	page, err := list(history, catalog).Handle(context.Background(), "customer-1", 10, "")
	require.NoError(t, err)
	assert.False(t, page.HasNextPage)
	assert.Equal(t, "c1", page.EndCursor)

	none := NewMockOrderHistory(t)
	none.EXPECT().ListCustomerOrders(mock.Anything, "customer-1", 10, "").Return(HistoryPage{}, nil).Once()
	empty, err := list(none, NewMockCatalog(t)).Handle(context.Background(), "customer-1", 10, "")
	require.NoError(t, err)
	assert.Equal(t, OrderPage{Edges: []OrderEdge{}}, empty)
}

func TestCustomerOrdersRefusals(t *testing.T) {
	h := list(NewMockOrderHistory(t), NewMockCatalog(t))
	for _, first := range []int{0, 101} {
		_, err := h.Handle(context.Background(), "customer-1", first, "")
		assert.ErrorIs(t, err, ErrInvalidPageSize)
	}
	invalid := NewMockOrderHistory(t)
	invalid.EXPECT().ListCustomerOrders(mock.Anything, "customer-1", 1, "x").Return(HistoryPage{}, ErrInvalidCursor).Once()
	_, err := list(invalid, NewMockCatalog(t)).Handle(context.Background(), "customer-1", 1, "x")
	assert.ErrorIs(t, err, ErrInvalidCursor)

	down := errors.New("restaurants is down")
	history := NewMockOrderHistory(t)
	history.EXPECT().ListCustomerOrders(mock.Anything, "customer-1", 1, "").Return(HistoryPage{Entries: []HistoryEntry{entry("order-1", "restaurant-1", "c1")}}, nil).Once()
	catalog := NewMockCatalog(t)
	catalog.EXPECT().Restaurants(mock.Anything, []string{"restaurant-1"}).Return(nil, down).Once()
	_, err = list(history, catalog).Handle(context.Background(), "customer-1", 1, "")
	assert.ErrorIs(t, err, down)

	broken := entry("order-1", "restaurant-1", "c1")
	broken.Order.State = "lost"
	brokenHistory := NewMockOrderHistory(t)
	brokenHistory.EXPECT().ListCustomerOrders(mock.Anything, "customer-1", 1, "").Return(HistoryPage{Entries: []HistoryEntry{broken}}, nil).Once()
	brokenCatalog := NewMockCatalog(t)
	brokenCatalog.EXPECT().Restaurants(mock.Anything, []string{"restaurant-1"}).Return(map[string]Restaurant{"restaurant-1": Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true}}, nil).Once()
	_, err = list(brokenHistory, brokenCatalog).Handle(context.Background(), "customer-1", 1, "")
	assert.Error(t, err, "an unknown state fails the page instead of being guessed")

	missingHistory := NewMockOrderHistory(t)
	missingHistory.EXPECT().ListCustomerOrders(mock.Anything, "customer-1", 1, "").Return(HistoryPage{Entries: []HistoryEntry{entry("order-1", "restaurant-1", "c1")}}, nil).Once()
	missingCatalog := NewMockCatalog(t)
	missingCatalog.EXPECT().Restaurants(mock.Anything, []string{"restaurant-1"}).Return(map[string]Restaurant{}, nil).Once()
	_, err = list(missingHistory, missingCatalog).Handle(context.Background(), "customer-1", 1, "")
	assert.Error(t, err, "a restaurant restaurants does not know fails the page")
}
