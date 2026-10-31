package place_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/place"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
)

var (
	windowStart = time.Date(2026, time.October, 25, 19, 0, 0, 0, time.UTC)
	windowEnd   = windowStart.Add(15 * time.Minute)
)

func deliveryWindow(t *testing.T, start time.Time) order.DeliveryWindow {
	t.Helper()
	window, err := order.NewDeliveryWindow(start, start.Add(15*time.Minute))
	require.NoError(t, err)
	return window
}

func scheduledInput() place.Input {
	input := validInput()
	input.DeliveryWindowStart = windowStart
	return input
}

func openSnapshot(t *testing.T) place.CatalogSnapshot {
	t.Helper()
	return place.CatalogSnapshot{
		MarketID: ordertest.MarketID(t),
		Currency: "EUR",
		Items: map[order.MenuItemID]place.CatalogItem{
			"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
			"soup":  {UnitPrice: ordertest.Money(t, "4.00"), Available: true},
		},
		MinimumTotal: ordertest.Money(t, "10"),
		MaximumTotal: ordertest.Money(t, "100"),
	}
}

// AC-914: Premium Priority is allowed with a window; the fee is part of total.
func TestPlaceOrderPremiumWithDeliveryWindow(t *testing.T) {
	e := newEnv(t)
	snapshot := openSnapshot(t)
	fee := ordertest.Money(t, "3.00")
	snapshot.PremiumFee = &fee
	e.catalog.EXPECT().
		Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
		Return(snapshot, nil).
		Once()
	e.slots.EXPECT().
		Reserve(mock.Anything, order.RestaurantID("restaurant-1"), windowStart, order.ID("order-1")).
		Return(deliveryWindow(t, windowStart), nil).
		Once()
	input := scheduledInput()
	input.Priority = string(order.PriorityPremium)

	_, err := e.handler.Handle(context.Background(), input)

	require.NoError(t, err)
	placed, ok := e.store.Snapshot("order-1")
	require.True(t, ok)
	assert.Equal(t, windowStart, placed.DeliveryWindow.Start())
	assert.Equal(t, order.PriorityPremium, placed.Service.Priority())
	restored, err := order.Restore(placed)
	require.NoError(t, err)
	assert.Equal(t, "32 EUR", restored.Total().String())
}

// The other refusals of the reservation store nothing, like the full window of
// place.feature.
func TestPlaceOrderRefusalOfTheReservationStoresNothing(t *testing.T) {
	for name, refusal := range map[string]struct {
		err   error
		check func(t *testing.T, err error)
	}{
		"scheduled orders unavailable": {
			err: fmt.Errorf("restaurants.ReserveScheduledSlot: %w", place.ErrScheduledOrdersUnavailable),
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, place.ErrScheduledOrdersUnavailable)
			},
		},
		"window off the grid": {
			err: fmt.Errorf("restaurants.ReserveScheduledSlot: %w", usecase.ErrInvalidInput),
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, usecase.ErrInvalidInput)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.catalog.EXPECT().
				Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
				Return(openSnapshot(t), nil).
				Once()
			e.slots.EXPECT().
				Reserve(mock.Anything, order.RestaurantID("restaurant-1"), windowStart, order.ID("order-1")).
				Return(order.DeliveryWindow{}, refusal.err).
				Once()

			_, err := e.handler.Handle(context.Background(), scheduledInput())

			refusal.check(t, err)
			assertNothingPlaced(t, e)
			assert.Empty(t, e.store.RelationshipChanges())
		})
	}
}

// AC-915: a repeat with the same idempotency key returns the stored Order and
// reserves nothing again.
func TestPlaceOrderScheduledReplayDoesNotReserveAgain(t *testing.T) {
	e := newEnv(t)
	e.catalog.EXPECT().
		Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
		Return(openSnapshot(t), nil).
		Once()
	e.slots.EXPECT().
		Reserve(mock.Anything, order.RestaurantID("restaurant-1"), windowStart, order.ID("order-1")).
		Return(deliveryWindow(t, windowStart), nil).
		Once()

	first, err := e.handler.Handle(context.Background(), scheduledInput())
	require.NoError(t, err)
	second, err := e.handler.Handle(context.Background(), scheduledInput())

	require.NoError(t, err)
	assert.Equal(t, place.Result{OrderID: first.OrderID, Replayed: true}, second)
	assert.Len(t, e.store.OutboxRecords(), 1)
}
