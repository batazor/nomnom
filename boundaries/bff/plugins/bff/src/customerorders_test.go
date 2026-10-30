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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/ordertracking/applications"
)

func customerOrdersService(t *testing.T, list *mockListCustomerOrdersHandler) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{accessTokens: verifier, tracking: &orderTracking{listCustomerOrders: list}}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func TestCustomerOrdersOfTheVerifiedCustomer(t *testing.T) {
	// the customer is the subject of the access token.
	const customer = "customer-1"
	list := newMockListCustomerOrdersHandler(t)
	list.EXPECT().Handle(mock.Anything, customer, 1, "c0").Return(applications.OrderPage{
		Edges: []applications.OrderEdge{{Cursor: "c1", Order: applications.OrderView{
			ID: "order-1", Status: applications.StatusCancelled, EndReason: applications.EndCustomerCancelled, Priority: applications.PriorityPremium,
			Restaurant: applications.Restaurant{ID: "restaurant-1", Name: "Кухня №8", IsOpen: true},
			Total:      applications.Money{Amount: "12.50", Currency: "RUB"},
			PlacedAt:   time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		}}},
		HasNextPage: true,
		EndCursor:   "c1",
	}, nil).Once()
	list.EXPECT().Handle(mock.Anything, customer, 1, "").Return(applications.OrderPage{Edges: []applications.OrderEdge{}}, nil).Once()
	svc, ctx := customerOrdersService(t, list)

	response, err := svc.QueryCustomerOrders(ctx, &service.QueryCustomerOrdersRequest{First: 1, After: wrapperspb.String("c0")})

	require.NoError(t, err)
	connection := response.GetCustomerOrders()
	require.Len(t, connection.GetEdges(), 1)
	assert.Equal(t, "c1", connection.GetEdges()[0].GetCursor())
	// AC-893: the node is the card of the order, order(id) reads the rest.
	node := connection.GetEdges()[0].GetNode()
	assert.Equal(t, "order-1", node.GetId())
	assert.Equal(t, service.OrderStatus_ORDER_STATUS_CANCELLED, node.GetStatus())
	assert.Equal(t, service.OrderEndReason_ORDER_END_REASON_CUSTOMER_CANCELLED, node.GetEndReason())
	assert.Equal(t, service.OrderPriority_ORDER_PRIORITY_PREMIUM, node.GetPriority())
	assert.Equal(t, "12.50", node.GetTotal().GetAmount())
	assert.Equal(t, "2026-10-25T12:00:00Z", node.GetPlacedAt())
	assert.Equal(t, "Кухня №8", node.GetRestaurant().GetName())
	assert.True(t, node.GetRestaurant().GetAcceptingOrders())
	assert.True(t, connection.GetPageInfo().GetHasNextPage())
	assert.Equal(t, "c1", connection.GetPageInfo().GetEndCursor().GetValue())

	response, err = svc.QueryCustomerOrders(ctx, &service.QueryCustomerOrdersRequest{First: 1})
	require.NoError(t, err)
	assert.Nil(t, response.GetCustomerOrders().GetPageInfo().GetEndCursor(), "an empty page has no end cursor")
}

func TestCustomerOrdersErrors(t *testing.T) {
	for refusal, code := range map[error]codes.Code{
		applications.ErrInvalidPageSize: codes.InvalidArgument,
		applications.ErrInvalidCursor:   codes.InvalidArgument,
		errors.New("orders is down"):    codes.Unavailable,
	} {
		list := newMockListCustomerOrdersHandler(t)
		list.EXPECT().Handle(mock.Anything, "customer-1", 1, "").Return(applications.OrderPage{}, refusal).Once()
		svc, ctx := customerOrdersService(t, list)
		_, err := svc.QueryCustomerOrders(ctx, &service.QueryCustomerOrdersRequest{First: 1})
		assert.Equal(t, code, status.Code(err), refusal.Error())
	}
	svc, _ := customerOrdersService(t, newMockListCustomerOrdersHandler(t))
	_, err := svc.QueryCustomerOrders(context.Background(), &service.QueryCustomerOrdersRequest{First: 1})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}
