package placeorder_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
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

func scheduledInput() placeorder.Input {
	input := validInput()
	input.DeliveryWindowStart = windowStart
	return input
}

func openSnapshot(t *testing.T) placeorder.CatalogSnapshot {
	t.Helper()
	return placeorder.CatalogSnapshot{
		MarketID: ordertest.MarketID(t),
		Currency: "EUR",
		Items: map[order.MenuItemID]placeorder.CatalogItem{
			"pizza": {UnitPrice: ordertest.Money(t, "12.50"), Available: true},
			"soup":  {UnitPrice: ordertest.Money(t, "4.00"), Available: true},
		},
		MinimumTotal: ordertest.Money(t, "10"),
		MaximumTotal: ordertest.Money(t, "100"),
	}
}

// AC-912: the window returned by the reservation is stored in the Order and
// carried by OrderPlaced.
func TestPlaceOrderReservesTheDeliveryWindow(t *testing.T) {
	e := newEnv(t)
	e.catalog.EXPECT().
		Snapshot(mock.Anything, order.RestaurantID("restaurant-1"), []order.MenuItemID{"pizza", "soup"}).
		Return(openSnapshot(t), nil).
		Once()
	e.slots.EXPECT().
		Reserve(mock.Anything, order.RestaurantID("restaurant-1"), windowStart, order.ID("order-1")).
		Return(deliveryWindow(t, windowStart), nil).
		Once()

	result, err := e.handler.Handle(context.Background(), scheduledInput())

	require.NoError(t, err)
	assert.Equal(t, placeorder.Result{OrderID: "order-1"}, result)
	placed, ok := e.store.Snapshot("order-1")
	require.True(t, ok)
	assert.Equal(t, windowStart, placed.DeliveryWindow.Start())
	assert.Equal(t, windowEnd, placed.DeliveryWindow.End())
	records := e.store.OutboxRecords()
	require.Len(t, records, 1)
	assert.Equal(t, repositorytest.ExpectedOutbox(t, "order-1", 1, order.OrderPlaced{
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		RestaurantID:   "restaurant-1",
		MarketID:       ordertest.MarketID(t),
		Total:          ordertest.Money(t, "29.00"),
		PlacedAt:       now,
		Items:          placed.Items,
		DeliveryWindow: deliveryWindow(t, windowStart),
	})[0], records[0])
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

// AC-913: a full window refuses the Order with alternatives; nothing is
// stored and the Outbox stays empty. The other refusals of the reservation
// pass through the same way.
func TestPlaceOrderRefusalOfTheReservationStoresNothing(t *testing.T) {
	alternatives := []order.DeliveryWindow{
		deliveryWindow(t, windowStart.Add(15*time.Minute)),
		deliveryWindow(t, windowStart.Add(30*time.Minute)),
	}
	for name, refusal := range map[string]struct {
		err   error
		check func(t *testing.T, err error)
	}{
		"slot unavailable": {
			err: placeorder.SlotUnavailable{Alternatives: alternatives},
			check: func(t *testing.T, err error) {
				var unavailable placeorder.SlotUnavailable
				require.ErrorAs(t, err, &unavailable)
				assert.Equal(t, alternatives, unavailable.Alternatives)
			},
		},
		"scheduled orders unavailable": {
			err: fmt.Errorf("restaurants.ReserveScheduledSlot: %w", placeorder.ErrScheduledOrdersUnavailable),
			check: func(t *testing.T, err error) {
				require.ErrorIs(t, err, placeorder.ErrScheduledOrdersUnavailable)
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
	assert.Equal(t, placeorder.Result{OrderID: first.OrderID, Replayed: true}, second)
	assert.Len(t, e.store.OutboxRecords(), 1)
}
