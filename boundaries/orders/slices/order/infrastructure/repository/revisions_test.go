package repository_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order/ordertest"
)

func TestAuthorizationRevision(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("rev")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))

	_, found, err := store.Revision(ctx, string(id))
	require.NoError(t, err)
	assert.False(t, found)
	require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
		return store.RememberRevision(ctx, id, "zed-token")
	}))

	revision, found, err := store.Revision(ctx, string(id))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, application.AuthorizationRevision("zed-token"), revision)
}
