package listcustomerorders_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/listcustomerorders"
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
