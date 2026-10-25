package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

func TestRepositoryUsesOptimisticConcurrency(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	require.NoError(t, store.Add(ctx, ordertest.New(t, "order-1")))
	require.ErrorIs(t, store.Add(ctx, ordertest.New(t, "order-1")), order.ErrOrderAlreadyExists)

	first, err := store.Get(ctx, "order-1")
	require.NoError(t, err)
	stale, err := store.Get(ctx, "order-1")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), first.Version())

	_, err = first.Confirm()
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, first))
	snapshot, _ := store.Snapshot("order-1")
	assert.Equal(t, uint64(2), snapshot.Version)

	_, err = stale.RecordPickup(time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, store.Save(ctx, stale), application.ErrConcurrencyConflict)

	_, err = store.Get(ctx, "missing")
	require.ErrorIs(t, err, order.ErrOrderNotFound)
	require.ErrorIs(t, store.Save(ctx, ordertest.New(t, "missing")), order.ErrOrderNotFound)
}
