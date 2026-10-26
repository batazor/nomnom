package repository_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInboxAcceptsEventOnce(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	eventID := string(uniqueID("event"))

	first, err := store.Accept(ctx, "orders.confirmorder", eventID)
	require.NoError(t, err)
	repeated, err := store.Accept(ctx, "orders.confirmorder", eventID)
	require.NoError(t, err)
	other, err := store.Accept(ctx, "orders.recordpickup", eventID)
	require.NoError(t, err)

	assert.True(t, first)
	assert.False(t, repeated)
	assert.True(t, other)
}
