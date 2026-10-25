//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

func TestIdempotencyKeys(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id, other := uniqueID("idem"), uniqueID("idem-other")
	customer := order.CustomerID(uniqueID("customer"))
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	require.NoError(t, store.Add(ctx, ordertest.New(t, other)))

	_, found, err := store.FindOrder(ctx, customer, "key-1")
	require.NoError(t, err)
	assert.False(t, found)
	require.NoError(t, store.RememberOrder(ctx, customer, "key-1", id))
	require.NoError(t, store.RememberOrder(ctx, customer, "key-1", id))
	require.ErrorIs(t, store.RememberOrder(ctx, customer, "key-1", other), postgres.ErrIdempotencyKeyTaken)

	found_, found, err := store.FindOrder(ctx, customer, "key-1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, id, found_)
}
