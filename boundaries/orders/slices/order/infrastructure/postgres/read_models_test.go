package postgres_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
)

func TestReadModels(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	customer := order.CustomerID(uniqueID("history"))
	var ids []order.ID
	for i := range 3 {
		id := uniqueID("list" + strconv.Itoa(i))
		ids = append(ids, id)
		require.NoError(t, store.Add(ctx, ordertest.New(t, id,
			ordertest.WithCustomer(customer), ordertest.WithPlacedAt(ordertest.PlacedAt.Add(time.Duration(i)*time.Minute)))))
	}

	view, err := store.OrderView(ctx, ids[0])
	require.NoError(t, err)
	assert.Equal(t, customer, view.CustomerID)
	require.Len(t, view.Items, 1)
	assert.Nil(t, view.Cancellation)

	first, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Orders, 2)
	assert.Equal(t, []order.ID{ids[2], ids[1]}, []order.ID{first.Orders[0].OrderID, first.Orders[1].OrderID})
	require.NotEmpty(t, first.NextCursor)
	assert.Equal(t, first.NextCursor, first.Orders[1].Cursor, "the last order continues the page")

	resumed, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2, Cursor: first.Orders[0].Cursor})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{ids[1], ids[0]}, []order.ID{resumed.Orders[0].OrderID, resumed.Orders[1].OrderID}, "any order cursor resumes after it")

	second, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Orders, 1)
	assert.Equal(t, ids[0], second.Orders[0].OrderID)
	assert.Empty(t, second.NextCursor)

	_, err = store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2, Cursor: "%%%"})
	require.ErrorIs(t, err, application.ErrInvalidInput)
}
