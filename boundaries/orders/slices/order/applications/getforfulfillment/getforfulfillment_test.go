package getforfulfillment_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/getforfulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

func TestHandleReturnsTheDataOfAnASAPOrder(t *testing.T) {
	store := repositorytest.Open(t)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1",
		ordertest.WithCustomer("customer-7"), ordertest.WithPremium(t, "3"))))

	view, err := getforfulfillment.NewHandler(store).Handle(context.Background(), getforfulfillment.Query{OrderID: "order-1"})

	require.NoError(t, err)
	assert.Equal(t, getforfulfillment.View{
		OrderID:      "order-1",
		CustomerID:   "customer-7",
		RestaurantID: "restaurant-1",
		MarketID:     ordertest.Market,
		Amount:       "23",
		Currency:     "EUR",
		Items:        []getforfulfillment.Item{{MenuItemID: "menu-1", Quantity: 2}},
		Priority:     "PREMIUM",
	}, view)
}

func TestHandleReturnsTheDeliveryWindowOfAScheduledOrder(t *testing.T) {
	store := repositorytest.Open(t)
	start := ordertest.PlacedAt.Add(4 * time.Hour)
	end := start.Add(15 * time.Minute)
	require.NoError(t, store.Add(context.Background(), ordertest.New(t, "order-1", ordertest.WithDeliveryWindow(t, start, end))))

	view, err := getforfulfillment.NewHandler(store).Handle(context.Background(), getforfulfillment.Query{OrderID: "order-1"})

	require.NoError(t, err)
	require.NotNil(t, view.DeliveryWindow)
	assert.True(t, start.Equal(view.DeliveryWindow.Start))
	assert.True(t, end.Equal(view.DeliveryWindow.End))
}

func TestHandleReportsAMissingOrder(t *testing.T) {
	store := repositorytest.Open(t)

	_, err := getforfulfillment.NewHandler(store).Handle(context.Background(), getforfulfillment.Query{OrderID: "missing"})

	assert.ErrorIs(t, err, order.ErrOrderNotFound)
}
