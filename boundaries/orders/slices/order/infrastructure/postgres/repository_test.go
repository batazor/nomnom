//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
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

	_, err = loaded.Confirm(ordertest.PlacedAt.Add(time.Minute))
	require.NoError(t, err)
	_, err = loaded.RecordPreparationStarted(ordertest.PlacedAt.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, loaded))

	saved, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), saved.Version())
	assert.Equal(t, order.StateConfirmed, saved.State())
	require.Len(t, saved.Facts(), 1)
	assert.Equal(t, order.FactPreparationStarted, saved.Facts()[0].Fact)
	assert.Equal(t, ordertest.New(t, id).Items(), saved.Items())
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
	require.ErrorIs(t, store.Save(ctx, second), application.ErrConcurrencyConflict)
	require.ErrorIs(t, store.Save(ctx, ordertest.New(t, uniqueID("missing"))), order.ErrOrderNotFound)
	_, err = store.Get(ctx, uniqueID("missing"))
	require.ErrorIs(t, err, order.ErrOrderNotFound)
}
