//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

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

// AC-671: a concurrent insert of the same key waits for the first transaction
// and, once it committed another Order, returns ErrIdempotencyKeyTaken; the
// committed Order is then visible to FindOrder (Place Order replays it).
func TestIdempotencyKeyConcurrentInsert(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	first, second := uniqueID("idem-first"), uniqueID("idem-second")
	customer := order.CustomerID(uniqueID("customer"))
	inserted, commit := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.Do(ctx, func(ctx context.Context) error {
			if err := store.Add(ctx, ordertest.New(t, first)); err != nil {
				return err
			}
			if err := store.RememberOrder(ctx, customer, "key-1", first); err != nil {
				return err
			}
			close(inserted)
			<-commit
			return nil
		})
	}()
	<-inserted

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- store.Do(ctx, func(ctx context.Context) error {
			if err := store.Add(ctx, ordertest.New(t, second)); err != nil {
				return err
			}
			return store.RememberOrder(ctx, customer, "key-1", second)
		})
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("the second insert did not wait for the first transaction: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(commit)
	require.NoError(t, <-firstDone)
	require.ErrorIs(t, <-secondDone, postgres.ErrIdempotencyKeyTaken)

	replayed, found, err := store.FindOrder(ctx, customer, "key-1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, first, replayed)
}
