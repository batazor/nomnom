package memory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/infrastructure/memory"
)

func TestIdempotencyKeyIdentifiesOneOrder(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	require.NoError(t, store.RememberOrder(ctx, "customer-1", "key-1", "order-1"))
	require.NoError(t, store.RememberOrder(ctx, "customer-1", "key-1", "order-1"))
	require.ErrorIs(t, store.RememberOrder(ctx, "customer-1", "key-1", "order-2"), memory.ErrIdempotencyKeyTaken)

	_, found, err := store.FindOrder(ctx, "customer-2", "key-1")
	require.NoError(t, err)
	assert.False(t, found, "keys are scoped by customer")
}
