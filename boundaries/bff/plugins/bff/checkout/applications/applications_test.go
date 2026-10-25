package applications

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tracking "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

type fakeOrders struct {
	orderID string
	err     error
	got     Input
}

func (f *fakeOrders) PlaceOrder(_ context.Context, input Input) (string, error) {
	f.got = input
	return f.orderID, f.err
}

type fakeViews struct {
	view tracking.OrderView
	err  error
	read string
}

func (f *fakeViews) Handle(_ context.Context, orderID string) (tracking.OrderView, error) {
	f.read = orderID
	return f.view, f.err
}

var input = Input{
	RestaurantID:   "restaurant-1",
	Lines:          []Line{{MenuItemID: "menu-pho", Quantity: 2}},
	IdempotencyKey: "attempt-1",
}

// AC-570: a placed Order comes back as the order tracking view.
func TestPlaceOrderReturnsThePlacedOrder(t *testing.T) {
	orders := &fakeOrders{orderID: "order-1"}
	views := &fakeViews{view: tracking.OrderView{ID: "order-1", Status: tracking.StatusReceived}}

	result, err := NewPlaceOrder(orders, views).Handle(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(t, input, orders.got)
	assert.Equal(t, "order-1", views.read)
	require.NotNil(t, result.Order)
	assert.Equal(t, tracking.StatusReceived, result.Order.Status)
	assert.Empty(t, result.Problems)
}

// AC-569: unavailable items are a problem with their ids, not an error.
func TestPlaceOrderTurnsRefusalsIntoProblems(t *testing.T) {
	cases := map[string]struct {
		refused PlacementRefused
		want    []Problem
	}{
		"unavailable item": {
			PlacementRefused{Reason: "ORDER_NOT_ORDERABLE", Codes: []string{"ORDER_ITEM_UNAVAILABLE"}, MenuItemIDs: []string{"menu-gyoza"}},
			[]Problem{{Code: ProblemOrderItemUnavailable, MenuItemIDs: []string{"menu-gyoza"}}},
		},
		"item and total": {
			PlacementRefused{Reason: "ORDER_NOT_ORDERABLE", Codes: []string{"ORDER_ITEM_UNAVAILABLE", "ORDER_TOTAL_BELOW_MINIMUM", "SOMETHING_NEW"}, MenuItemIDs: []string{"menu-gyoza", "menu-tea"}},
			[]Problem{
				{Code: ProblemOrderItemUnavailable, MenuItemIDs: []string{"menu-gyoza", "menu-tea"}},
				{Code: ProblemOrderTotalBelowMinimum, MenuItemIDs: []string{}},
			},
		},
		"closed restaurant": {
			PlacementRefused{Reason: "RESTAURANT_NOT_ACCEPTING_ORDERS"},
			[]Problem{{Code: ProblemRestaurantNotAcceptingOrders, MenuItemIDs: []string{}}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			views := &fakeViews{}
			result, err := NewPlaceOrder(&fakeOrders{err: tc.refused}, views).Handle(context.Background(), input)

			require.NoError(t, err)
			assert.Nil(t, result.Order)
			assert.Equal(t, tc.want, result.Problems)
			assert.Empty(t, views.read, "no order to read")
		})
	}
}

func TestPlaceOrderKeepsUnnamedRefusalsAndFailuresAsErrors(t *testing.T) {
	unknown := PlacementRefused{Reason: "ORDER_NOT_ORDERABLE", Codes: []string{"SOMETHING_NEW"}}
	_, err := NewPlaceOrder(&fakeOrders{err: unknown}, &fakeViews{}).Handle(context.Background(), input)
	assert.ErrorAs(t, err, new(PlacementRefused))

	_, err = NewPlaceOrder(&fakeOrders{err: ErrRestaurantNotFound}, &fakeViews{}).Handle(context.Background(), input)
	assert.ErrorIs(t, err, ErrRestaurantNotFound)

	unreadable := errors.New("orders unavailable")
	_, err = NewPlaceOrder(&fakeOrders{orderID: "order-1"}, &fakeViews{err: unreadable}).Handle(context.Background(), input)
	assert.ErrorIs(t, err, unreadable)
}
