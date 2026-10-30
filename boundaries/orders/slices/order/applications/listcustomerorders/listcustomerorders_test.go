package listcustomerorders_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// window is the Cancellation Window of the fixed policy of the tests.
const window = 5 * time.Minute

var (
	customer = authz.Subject{Kind: authz.SubjectUser, ID: "customer-1"}
	stranger = authz.Subject{Kind: authz.SubjectUser, ID: "customer-2"}
	support  = authz.Subject{Kind: authz.SubjectService, ID: "support-console"}
)

func setup(t *testing.T) (listcustomerorders.Handler, *MockAuthorizer) {
	t.Helper()
	store := repositorytest.Open(t)
	ctx := context.Background()
	for i := range 3 {
		placedAt := ordertest.PlacedAt.Add(time.Duration(i) * time.Hour)
		id := order.ID([]string{"order-a", "order-b", "order-c"}[i])
		require.NoError(t, store.Add(ctx, ordertest.New(t, id, ordertest.WithPlacedAt(placedAt))))
	}
	require.NoError(t, store.Add(ctx, ordertest.New(t, "order-x", ordertest.WithCustomer("customer-2"))))
	require.NoError(t, store.Add(ctx, ordertest.New(t, "order-h", ordertest.WithMarket("hamburg"),
		ordertest.WithPlacedAt(ordertest.PlacedAt.Add(time.Hour+30*time.Minute)))))
	authorizer := NewMockAuthorizer(t)
	policy, err := cancellationpolicy.NewFixed(window, time.Hour)
	require.NoError(t, err)
	return listcustomerorders.NewHandler(authorizer, store, policy), authorizer
}

func ids(page listcustomerorders.Page) []order.ID {
	result := make([]order.ID, 0, len(page.Orders))
	for _, summary := range page.Orders {
		result = append(result, summary.OrderID)
	}
	return result
}

// AC-128: the Customer reads only own orders, newest first, without the authorization graph.
func TestListOwnOrdersNewestFirstWithPagination(t *testing.T) {
	// The authorizer has no expectation: any permission check fails the test.
	handler, _ := setup(t)

	first, err := handler.Handle(context.Background(), listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", MarketID: ordertest.Market, Limit: 2})
	require.NoError(t, err)
	second, err := handler.Handle(context.Background(), listcustomerorders.Query{
		Subject: customer, CustomerID: "customer-1", MarketID: ordertest.Market, Limit: 2, Cursor: first.NextCursor,
	})
	require.NoError(t, err)

	assert.Equal(t, []order.ID{"order-c", "order-b"}, ids(first))
	assert.Equal(t, []order.ID{"order-a"}, ids(second))
	assert.Empty(t, second.NextCursor)
	last := second.Orders[0]
	assert.NotEmpty(t, last.Cursor, "every order carries the cursor that continues after it")
	last.Cursor = ""
	assert.Equal(t, listcustomerorders.Entry{View: orderdetails.View{
		Details: orderdetails.Details{
			OrderID:      "order-a",
			CustomerID:   "customer-1",
			MarketID:     ordertest.MarketID(t),
			RestaurantID: "restaurant-1",
			Items:        []orderdetails.Item{{MenuItemID: "menu-1", Name: "Margherita", Quantity: 2, UnitPrice: ordertest.Money(t, "10")}},
			Service:      order.StandardService(),
			Total:        ordertest.Money(t, "20"),
			State:        order.StatePlaced,
			PlacedAt:     ordertest.PlacedAt,
			Transitions:  []order.AppliedTransition{},
		},
		CancellationWindow: &order.CancellationWindow{OpensAt: ordertest.PlacedAt, ClosesAt: ordertest.PlacedAt.Add(window)},
	}}, last, "the history carries the full card with its Cancellation Window")
}

// AC-129: another subject needs the permission to view the Customer's orders.
func TestListOtherCustomerOrdersRequiresPermission(t *testing.T) {
	handler, authorizer := setup(t)
	authorizer.EXPECT().
		Check(mock.Anything, authz.AccessCheck{
			Subject:    stranger,
			Permission: listcustomerorders.PermissionViewOrders,
			Resource:   authz.Resource{Type: listcustomerorders.ResourceCustomer, ID: "customer-1"},
		}).
		Return(authz.ErrPermissionDenied).
		Once()
	authorizer.EXPECT().
		Check(mock.Anything, authz.AccessCheck{
			Subject:    support,
			Permission: listcustomerorders.PermissionViewOrders,
			Resource:   authz.Resource{Type: listcustomerorders.ResourceCustomer, ID: "customer-1"},
		}).
		Return(nil).
		Once()

	_, err := handler.Handle(context.Background(), listcustomerorders.Query{Subject: stranger, CustomerID: "customer-1", MarketID: ordertest.Market})
	require.ErrorIs(t, err, authz.ErrPermissionDenied)

	page, err := handler.Handle(context.Background(), listcustomerorders.Query{Subject: support, CustomerID: "customer-1", MarketID: ordertest.Market})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{"order-c", "order-b", "order-a"}, ids(page))
}

func TestListCustomerOrdersValidatesQuery(t *testing.T) {
	handler, _ := setup(t)
	tests := []struct {
		name  string
		query listcustomerorders.Query
	}{
		{name: "no customer", query: listcustomerorders.Query{Subject: customer, MarketID: ordertest.Market}},
		{name: "no market", query: listcustomerorders.Query{Subject: customer, CustomerID: "customer-1"}},
		{name: "malformed market", query: listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", MarketID: "Berlin"}},
		{name: "negative limit", query: listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", MarketID: ordertest.Market, Limit: -1}},
		{name: "limit above maximum", query: listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", MarketID: ordertest.Market, Limit: listcustomerorders.MaxLimit + 1}},
		{name: "malformed cursor", query: listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", MarketID: ordertest.Market, Cursor: "?"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := handler.Handle(context.Background(), tt.query)
			require.ErrorIs(t, err, usecase.ErrInvalidInput)
		})
	}
}

// ADR 41: the history of a Customer is read per Market; Orders of another
// Market are not on its pages.
func TestListCustomerOrdersFiltersByMarket(t *testing.T) {
	handler, _ := setup(t)

	hamburg, err := handler.Handle(context.Background(), listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", MarketID: "hamburg"})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{"order-h"}, ids(hamburg))
	assert.Equal(t, "hamburg", hamburg.Orders[0].MarketID.String())

	first, err := handler.Handle(context.Background(), listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", MarketID: ordertest.Market, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{"order-c", "order-b"}, ids(first), "order-h sits between them by placed_at but is in another Market")

	empty, err := handler.Handle(context.Background(), listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", MarketID: "paris"})
	require.NoError(t, err)
	assert.Empty(t, empty.Orders)
}
