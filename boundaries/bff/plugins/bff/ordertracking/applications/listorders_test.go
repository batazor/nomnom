package applications

import (
	"context"
	"errors"
	"testing"

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

type fakeViews map[string]error

func (f fakeViews) Handle(_ context.Context, orderID string) (OrderView, error) {
	if err := f[orderID]; err != nil {
		return OrderView{}, err
	}
	return OrderView{ID: orderID}, nil
}

func TestCustomerOrdersAreFullOrderViewsWithTheirCursors(t *testing.T) {
	history := &fakeHistory{page: HistoryPage{
		Entries:    []HistoryEntry{{OrderID: "order-3", Cursor: "c3"}, {OrderID: "order-gone", Cursor: "cg"}, {OrderID: "order-2", Cursor: "c2"}},
		NextCursor: "c2",
	}}

	page, err := NewListCustomerOrders(history, fakeViews{"order-gone": ErrOrderNotFound}).Handle(context.Background(), "customer-1", 3, "c4")

	require.NoError(t, err)
	assert.Equal(t, []any{"customer-1", 3, "c4"}, history.got)
	assert.Equal(t, OrderPage{
		Edges:       []OrderEdge{{Cursor: "c3", Order: OrderView{ID: "order-3"}}, {Cursor: "c2", Order: OrderView{ID: "order-2"}}},
		HasNextPage: true,
		EndCursor:   "c2",
	}, page, "an order gone between the reads is left out")
}

func TestLastHistoryPage(t *testing.T) {
	history := &fakeHistory{page: HistoryPage{Entries: []HistoryEntry{{OrderID: "order-1", Cursor: "c1"}}}}
	page, err := NewListCustomerOrders(history, fakeViews{}).Handle(context.Background(), "customer-1", 10, "")
	require.NoError(t, err)
	assert.False(t, page.HasNextPage)
	assert.Equal(t, "c1", page.EndCursor)

	empty, err := NewListCustomerOrders(&fakeHistory{}, fakeViews{}).Handle(context.Background(), "customer-1", 10, "")
	require.NoError(t, err)
	assert.Equal(t, OrderPage{Edges: []OrderEdge{}}, empty)
}

func TestCustomerOrdersRefusals(t *testing.T) {
	h := NewListCustomerOrders(&fakeHistory{}, fakeViews{})
	for _, first := range []int{0, 101} {
		_, err := h.Handle(context.Background(), "customer-1", first, "")
		assert.ErrorIs(t, err, ErrInvalidPageSize)
	}
	_, err := NewListCustomerOrders(&fakeHistory{err: ErrInvalidCursor}, fakeViews{}).Handle(context.Background(), "customer-1", 1, "x")
	assert.ErrorIs(t, err, ErrInvalidCursor)

	down := errors.New("orders is down")
	history := &fakeHistory{page: HistoryPage{Entries: []HistoryEntry{{OrderID: "order-1"}}}}
	_, err = NewListCustomerOrders(history, fakeViews{"order-1": down}).Handle(context.Background(), "customer-1", 1, "")
	assert.ErrorIs(t, err, down)
}
