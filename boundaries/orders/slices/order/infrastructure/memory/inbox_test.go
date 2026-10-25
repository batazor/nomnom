package memory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/infrastructure/memory"
)

func TestInboxAcceptsEachConsumerEventOnce(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()

	first, err := store.Accept(ctx, "orders.confirmorder", "event-1")
	require.NoError(t, err)
	again, err := store.Accept(ctx, "orders.confirmorder", "event-1")
	require.NoError(t, err)
	other, err := store.Accept(ctx, "orders.recordpickup", "event-1")
	require.NoError(t, err)

	assert.True(t, first)
	assert.False(t, again)
	assert.True(t, other, "consumers deduplicate independently")
}
