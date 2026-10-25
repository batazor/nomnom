package listcustomerorders_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/fake"
	"nomnom/orders/slices/order/infrastructure/postgres/postgrestest"
)

var (
	customer = application.Subject{Kind: application.SubjectUser, ID: "customer-1"}
	stranger = application.Subject{Kind: application.SubjectUser, ID: "customer-2"}
	support  = application.Subject{Kind: application.SubjectService, ID: "support-console"}
)

func setup(t *testing.T) (listcustomerorders.Handler, *fake.Authorizer) {
	t.Helper()
	store := postgrestest.Open(t)
	ctx := context.Background()
	for i := range 3 {
		placedAt := ordertest.PlacedAt.Add(time.Duration(i) * time.Hour)
		id := order.ID([]string{"order-a", "order-b", "order-c"}[i])
		require.NoError(t, store.Add(ctx, ordertest.New(t, id, ordertest.WithPlacedAt(placedAt))))
	}
	require.NoError(t, store.Add(ctx, ordertest.New(t, "order-x", ordertest.WithCustomer("customer-2"))))
	authorizer := fake.NewAuthorizer()
	return listcustomerorders.NewHandler(authorizer, store), authorizer
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
	handler, authorizer := setup(t)

	first, err := handler.Handle(context.Background(), listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", Limit: 2})
	require.NoError(t, err)
	second, err := handler.Handle(context.Background(), listcustomerorders.Query{
		Subject: customer, CustomerID: "customer-1", Limit: 2, Cursor: first.NextCursor,
	})
	require.NoError(t, err)

	assert.Equal(t, []order.ID{"order-c", "order-b"}, ids(first))
	assert.Equal(t, []order.ID{"order-a"}, ids(second))
	assert.Empty(t, second.NextCursor)
	last := second.Orders[0]
	assert.NotEmpty(t, last.Cursor, "every order carries the cursor that continues after it")
	last.Cursor = ""
	assert.Equal(t, listcustomerorders.OrderSummary{
		OrderID:      "order-a",
		RestaurantID: "restaurant-1",
		State:        order.StatePlaced,
		Total:        ordertest.Money(t, "20"),
		PlacedAt:     ordertest.PlacedAt,
	}, last)
	assert.Empty(t, authorizer.Checks())
}

// AC-129: another subject needs the permission to view the Customer's orders.
func TestListOtherCustomerOrdersRequiresPermission(t *testing.T) {
	handler, authorizer := setup(t)
	authorizer.Allow(support, listcustomerorders.PermissionViewOrders,
		application.Resource{Type: listcustomerorders.ResourceCustomer, ID: "customer-1"})

	_, err := handler.Handle(context.Background(), listcustomerorders.Query{Subject: stranger, CustomerID: "customer-1"})
	require.ErrorIs(t, err, application.ErrPermissionDenied)

	page, err := handler.Handle(context.Background(), listcustomerorders.Query{Subject: support, CustomerID: "customer-1"})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{"order-c", "order-b", "order-a"}, ids(page))
}

func TestListCustomerOrdersValidatesQuery(t *testing.T) {
	handler, _ := setup(t)
	tests := []struct {
		name  string
		query listcustomerorders.Query
	}{
		{name: "no customer", query: listcustomerorders.Query{Subject: customer}},
		{name: "negative limit", query: listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", Limit: -1}},
		{name: "limit above maximum", query: listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", Limit: listcustomerorders.MaxLimit + 1}},
		{name: "malformed cursor", query: listcustomerorders.Query{Subject: customer, CustomerID: "customer-1", Cursor: "?"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := handler.Handle(context.Background(), tt.query)
			require.ErrorIs(t, err, application.ErrInvalidInput)
		})
	}
}
