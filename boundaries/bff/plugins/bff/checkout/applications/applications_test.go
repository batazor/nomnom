package applications

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	tracking "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

var input = Input{
	RestaurantID:   "restaurant-1",
	Lines:          []Line{{MenuItemID: "menu-pho", Quantity: 2}},
	IdempotencyKey: "attempt-1",
}

// AC-570: a placed Order comes back as the order tracking view.
func TestPlaceOrderReturnsThePlacedOrder(t *testing.T) {
	orders := NewMockOrderPlacer(t)
	orders.EXPECT().PlaceOrder(mock.Anything, input).Return("order-1", nil).Once()
	views := NewMockOrderViews(t)
	views.EXPECT().Handle(mock.Anything, "order-1").Return(tracking.OrderView{ID: "order-1", Status: tracking.StatusReceived}, nil).Once()

	result, err := NewPlaceOrder(orders, views).Handle(context.Background(), input)

	require.NoError(t, err)
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
			orders := NewMockOrderPlacer(t)
			orders.EXPECT().PlaceOrder(mock.Anything, input).Return("", tc.refused).Once()

			// No order to read: views expects no call.
			result, err := NewPlaceOrder(orders, NewMockOrderViews(t)).Handle(context.Background(), input)

			require.NoError(t, err)
			assert.Nil(t, result.Order)
			assert.Equal(t, tc.want, result.Problems)
		})
	}
}

func TestPlaceOrderKeepsUnnamedRefusalsAndFailuresAsErrors(t *testing.T) {
	unknown := PlacementRefused{Reason: "ORDER_NOT_ORDERABLE", Codes: []string{"SOMETHING_NEW"}}
	refusing := NewMockOrderPlacer(t)
	refusing.EXPECT().PlaceOrder(mock.Anything, input).Return("", unknown).Once()
	_, err := NewPlaceOrder(refusing, NewMockOrderViews(t)).Handle(context.Background(), input)
	assert.ErrorAs(t, err, new(PlacementRefused))

	missing := NewMockOrderPlacer(t)
	missing.EXPECT().PlaceOrder(mock.Anything, input).Return("", ErrRestaurantNotFound).Once()
	_, err = NewPlaceOrder(missing, NewMockOrderViews(t)).Handle(context.Background(), input)
	assert.ErrorIs(t, err, ErrRestaurantNotFound)

	unreadable := errors.New("orders unavailable")
	views := NewMockOrderViews(t)
	views.EXPECT().Handle(mock.Anything, "order-1").Return(tracking.OrderView{}, unreadable).Once()
	placed := NewMockOrderPlacer(t)
	placed.EXPECT().PlaceOrder(mock.Anything, input).Return("order-1", nil).Once()
	_, err = NewPlaceOrder(placed, views).Handle(context.Background(), input)
	assert.ErrorIs(t, err, unreadable)
}
