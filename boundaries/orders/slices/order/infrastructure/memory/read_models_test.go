package memory_test

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
	"nomnom/orders/slices/order/infrastructure/memory"
)

func TestListCustomerOrdersPaginatesNewestFirst(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	for i, id := range []order.ID{"order-a", "order-b", "order-c"} {
		placedAt := ordertest.PlacedAt.Add(time.Duration(i) * time.Minute)
		require.NoError(t, store.Add(ctx, ordertest.New(t, id, ordertest.WithPlacedAt(placedAt))))
	}
	require.NoError(t, store.Add(ctx, ordertest.New(t, "foreign", ordertest.WithCustomer("customer-2"))))

	first, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: "customer-1", Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{"order-c", "order-b"}, ids(first))
	require.NotEmpty(t, first.NextCursor)

	second, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: "customer-1", Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{"order-a"}, ids(second))
	assert.Empty(t, second.NextCursor)
	assert.Equal(t, "20 EUR", second.Orders[0].Total.String())

	_, err = store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: "customer-1", Limit: 2, Cursor: "x"})
	require.ErrorIs(t, err, application.ErrInvalidInput)
}

func ids(page listcustomerorders.Page) []order.ID {
	result := make([]order.ID, 0, len(page.Orders))
	for _, summary := range page.Orders {
		result = append(result, summary.OrderID)
	}
	return result
}
