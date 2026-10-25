package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

type fakeCustomerOrders struct {
	page     applications.OrderPage
	err      error
	customer string
	first    int
	after    string
}

func (f *fakeCustomerOrders) Handle(_ context.Context, customerID string, first int, after string) (applications.OrderPage, error) {
	f.customer, f.first, f.after = customerID, first, after
	return f.page, f.err
}

func customerOrdersService(t *testing.T, list *fakeCustomerOrders) (*BffService, context.Context) {
	t.Helper()
	verifier, token := accessToken(t)
	svc := &BffService{accessTokens: verifier, tracking: &orderTracking{listCustomerOrders: list}}
	return svc, metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func TestCustomerOrdersOfTheVerifiedCustomer(t *testing.T) {
	list := &fakeCustomerOrders{page: applications.OrderPage{
		Edges: []applications.OrderEdge{{Cursor: "c1", Order: applications.OrderView{
			ID: "order-1", Status: applications.StatusReceived, PlacedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		}}},
		HasNextPage: true,
		EndCursor:   "c1",
	}}
	svc, ctx := customerOrdersService(t, list)

	response, err := svc.QueryCustomerOrders(ctx, &service.QueryCustomerOrdersRequest{First: 1, After: wrapperspb.String("c0")})

	require.NoError(t, err)
	assert.NotEmpty(t, list.customer, "the customer is the subject of the access token")
	assert.Equal(t, 1, list.first)
	assert.Equal(t, "c0", list.after)
	connection := response.GetCustomerOrders()
	require.Len(t, connection.GetEdges(), 1)
	assert.Equal(t, "c1", connection.GetEdges()[0].GetCursor())
	assert.Equal(t, "order-1", connection.GetEdges()[0].GetNode().GetId())
	assert.Equal(t, service.OrderStatus_ORDER_STATUS_RECEIVED, connection.GetEdges()[0].GetNode().GetStatus())
	assert.True(t, connection.GetPageInfo().GetHasNextPage())
	assert.Equal(t, "c1", connection.GetPageInfo().GetEndCursor().GetValue())

	list.page = applications.OrderPage{Edges: []applications.OrderEdge{}}
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
		svc, ctx := customerOrdersService(t, &fakeCustomerOrders{err: refusal})
		_, err := svc.QueryCustomerOrders(ctx, &service.QueryCustomerOrdersRequest{First: 1})
		assert.Equal(t, code, status.Code(err), refusal.Error())
	}
	svc, _ := customerOrdersService(t, &fakeCustomerOrders{})
	_, err := svc.QueryCustomerOrders(context.Background(), &service.QueryCustomerOrdersRequest{First: 1})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}
