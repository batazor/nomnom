package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	tracking "github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

func checkoutService(t *testing.T, placeOrder *mockPlaceOrderHandler) (*BffService, context.Context) {
	t.Helper()
	svc, ctx := newService(t, newMockGetOrderHandler(t), newMockRequestCancellationHandler(t))
	svc.checkout = &checkout{placeOrder: placeOrder}
	return svc, ctx
}

func placeOrderRequest() *service.MutationPlaceOrderRequest {
	return &service.MutationPlaceOrderRequest{Input: &service.PlaceOrderInput{
		RestaurantId:   "restaurant-1",
		Items:          []*service.OrderLineInput{{MenuItemId: "menu-pho", Quantity: 2}},
		IdempotencyKey: "attempt-1",
	}}
}

func TestPlaceOrderRequiresAccessToken(t *testing.T) {
	svc, _ := checkoutService(t, newMockPlaceOrderHandler(t))

	_, err := svc.MutationPlaceOrder(context.Background(), placeOrderRequest())

	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// AC-570: a placed order comes back RECEIVED without problems.
func TestPlaceOrderReturnsTheOrder(t *testing.T) {
	placed := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	placeOrder := newMockPlaceOrderHandler(t)
	// orders is called on behalf of the customer.
	placeOrder.EXPECT().Handle(mock.MatchedBy(func(ctx context.Context) bool {
		user, ok := identity.UserFrom(ctx)
		return ok && user.ID == "customer-1"
	}), applications.Input{
		RestaurantID: "restaurant-1", Lines: []applications.Line{{MenuItemID: "menu-pho", Quantity: 2}},
		IdempotencyKey: "attempt-1",
	}).Return(applications.Result{Order: &tracking.OrderView{
		ID: "order-1", Status: tracking.StatusReceived, PlacedAt: placed,
		Restaurant: tracking.Restaurant{ID: "restaurant-1", Name: "Pho Bar", IsOpen: true},
		Total:      tracking.Money{Amount: "19.00", Currency: "EUR"},
	}}, nil).Once()
	svc, ctx := checkoutService(t, placeOrder)

	response, err := svc.MutationPlaceOrder(ctx, placeOrderRequest())

	require.NoError(t, err)
	payload := response.GetPlaceOrder()
	assert.Equal(t, service.OrderStatus_ORDER_STATUS_RECEIVED, payload.GetOrder().GetStatus())
	assert.Equal(t, "19.00", payload.GetOrder().GetTotal().GetAmount())
	assert.Empty(t, payload.GetProblems())
}

// AC-569: problems replace the order, without a GraphQL error.
func TestPlaceOrderReturnsProblems(t *testing.T) {
	placeOrder := newMockPlaceOrderHandler(t)
	placeOrder.EXPECT().Handle(mock.Anything, mock.Anything).Return(applications.Result{Problems: []applications.Problem{
		{Code: applications.ProblemOrderItemUnavailable, MenuItemIDs: []string{"menu-gyoza"}},
		{Code: applications.ProblemRestaurantNotAcceptingOrders, MenuItemIDs: []string{}},
	}}, nil).Once()
	svc, ctx := checkoutService(t, placeOrder)

	response, err := svc.MutationPlaceOrder(ctx, placeOrderRequest())

	require.NoError(t, err)
	payload := response.GetPlaceOrder()
	assert.Nil(t, payload.GetOrder())
	require.Len(t, payload.GetProblems(), 2)
	assert.Equal(t, service.PlaceOrderProblemCode_PLACE_ORDER_PROBLEM_CODE_ORDER_ITEM_UNAVAILABLE, payload.GetProblems()[0].GetCode())
	assert.Equal(t, []string{"menu-gyoza"}, payload.GetProblems()[0].GetMenuItemIds())
	assert.Equal(t, service.PlaceOrderProblemCode_PLACE_ORDER_PROBLEM_CODE_RESTAURANT_NOT_ACCEPTING_ORDERS, payload.GetProblems()[1].GetCode())
}

func TestPlaceOrderErrors(t *testing.T) {
	cases := map[string]struct {
		err  error
		want codes.Code
	}{
		"unknown restaurant": {applications.ErrRestaurantNotFound, codes.NotFound},
		"invalid order":      {applications.ErrInvalidOrder, codes.InvalidArgument},
		"orders unavailable": {errors.New("down"), codes.Unavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			placeOrder := newMockPlaceOrderHandler(t)
			placeOrder.EXPECT().Handle(mock.Anything, mock.Anything).Return(applications.Result{}, tc.err).Once()
			svc, ctx := checkoutService(t, placeOrder)
			_, err := svc.MutationPlaceOrder(ctx, placeOrderRequest())
			assert.Equal(t, tc.want, status.Code(err))
		})
	}

	svc, ctx := checkoutService(t, newMockPlaceOrderHandler(t))
	request := placeOrderRequest()
	request.Input.IdempotencyKey = ""
	_, err := svc.MutationPlaceOrder(ctx, request)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

}
