package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
)

func TestRepositoryRoundTrip(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("rt")

	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	loaded, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), loaded.Version())
	assert.Equal(t, order.StatePlaced, loaded.State())
	assert.Equal(t, "20", loaded.Total().Amount().String())

	acceptedAt := ordertest.PlacedAt.Add(time.Minute)
	preparingAt := ordertest.PlacedAt.Add(2 * time.Minute)
	_, err = loaded.Confirm(acceptedAt)
	require.NoError(t, err)
	_, err = loaded.RecordPreparationStarted(preparingAt)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, loaded))

	saved, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), saved.Version())
	assert.Equal(t, order.StatePreparing, saved.State())
	// Confirm records restaurant_accepted with the transition (AC-563); facts
	// load in the order they occurred.
	assert.Equal(t, []order.RecordedFact{
		{Fact: order.FactRestaurantAccepted, OccurredAt: acceptedAt},
		{Fact: order.FactPreparationStarted, OccurredAt: preparingAt},
	}, saved.Facts())
	assert.Equal(t, ordertest.New(t, id).Items(), saved.Items())
}

// AC-856: the priority and the premium fee fixed at Place Order are stored
// with the Order and its details projection.
func TestRepositoryKeepsPremiumPriority(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	premium, standard := uniqueID("premium"), uniqueID("standard")

	require.NoError(t, store.Add(ctx, ordertest.New(t, premium, ordertest.WithPremium(t, "1.50"))))
	require.NoError(t, store.Add(ctx, ordertest.New(t, standard)))

	loaded, err := store.Get(ctx, premium)
	require.NoError(t, err)
	assert.Equal(t, order.PriorityPremium, loaded.Service().Priority())
	fee, ok := loaded.Service().PremiumFee()
	require.True(t, ok)
	assert.True(t, fee.Equal(ordertest.Money(t, "1.50")))
	assert.Equal(t, "21.5", loaded.Total().Amount().String())
	details, err := store.OrderDetails(ctx, premium)
	require.NoError(t, err)
	assert.Equal(t, loaded.Service(), details.Service)

	plain, err := store.Get(ctx, standard)
	require.NoError(t, err)
	assert.Equal(t, order.StandardService(), plain.Service())
}

// FR-681: the Delivery Window of a Scheduled Order is stored with the Order
// and its details projection; an ASAP Order has none.
func TestRepositoryKeepsDeliveryWindow(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	scheduled, asap := uniqueID("scheduled"), uniqueID("asap")
	start := ordertest.PlacedAt.Add(7 * time.Hour)
	require.NoError(t, store.Add(ctx, ordertest.New(t, scheduled, ordertest.WithDeliveryWindow(t, start, start.Add(15*time.Minute)))))
	require.NoError(t, store.Add(ctx, ordertest.New(t, asap)))

	loaded, err := store.Get(ctx, scheduled)
	require.NoError(t, err)
	assert.Equal(t, start, loaded.DeliveryWindow().Start())
	assert.Equal(t, start.Add(15*time.Minute), loaded.DeliveryWindow().End())
	details, err := store.OrderDetails(ctx, scheduled)
	require.NoError(t, err)
	assert.Equal(t, loaded.DeliveryWindow(), details.DeliveryWindow)

	plain, err := store.Get(ctx, asap)
	require.NoError(t, err)
	assert.True(t, plain.DeliveryWindow().IsZero())
	plainDetails, err := store.OrderDetails(ctx, asap)
	require.NoError(t, err)
	assert.True(t, plainDetails.DeliveryWindow.IsZero())
}

func TestRepositoryKeepsCancellation(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("cancel")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))

	loaded, err := store.Get(ctx, id)
	require.NoError(t, err)
	reason, err := order.NewCancellationReason(order.OutcomeFailure, "delivery_failed")
	require.NoError(t, err)
	cancelledAt := ordertest.PlacedAt.Add(time.Hour)
	_, err = loaded.Cancel(cancelledAt, reason)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, loaded))

	saved, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, order.StateCancelled, saved.State())
	assert.Equal(t, reason, saved.CancellationReason())
	assert.Equal(t, cancelledAt, saved.CancelledAt())
}

// AC-468: the second writer of the same version gets a concurrency conflict.
func TestRepositoryOptimisticConcurrency(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("cc")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	require.ErrorIs(t, store.Add(ctx, ordertest.New(t, id)), order.ErrOrderAlreadyExists)

	first, err := store.Get(ctx, id)
	require.NoError(t, err)
	second, err := store.Get(ctx, id)
	require.NoError(t, err)
	_, err = first.Confirm(ordertest.PlacedAt.Add(time.Minute))
	require.NoError(t, err)
	_, err = second.Confirm(ordertest.PlacedAt.Add(time.Minute))
	require.NoError(t, err)

	require.NoError(t, store.Save(ctx, first))
	require.ErrorIs(t, store.Save(ctx, second), usecase.ErrConcurrencyConflict)
	require.ErrorIs(t, store.Save(ctx, ordertest.New(t, uniqueID("missing"))), order.ErrOrderNotFound)
	_, err = store.Get(ctx, uniqueID("missing"))
	require.ErrorIs(t, err, order.ErrOrderNotFound)
}
