//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

// open opens a store on the database migrated by TestMain.
func open(t *testing.T) *postgres.Store {
	t.Helper()
	store, err := postgres.Open(context.Background(), config.PostgresConfig{DSN: pg.DSN, MaxConns: 5})
	require.NoError(t, err)
	t.Cleanup(store.Close)
	return store
}

func uniqueID(prefix string) order.ID {
	return order.ID(prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36))
}

// AC-469: an error after Append rolls back the Order and its Outbox record.
func TestUnitOfWorkRollsBackOrderAndOutbox(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("rb")
	placed := ordertest.New(t, id)
	failure := errors.New("use case failed")

	err := store.Do(ctx, func(ctx context.Context) error {
		require.NoError(t, store.Add(ctx, placed))
		require.NoError(t, store.Append(ctx, id, 1, []order.DomainEvent{order.OrderCompleted{OrderID: id}}))
		return failure
	})

	require.ErrorIs(t, err, failure)
	_, err = store.Get(ctx, id)
	require.ErrorIs(t, err, order.ErrOrderNotFound)
	assert.Zero(t, outboxCount(t, store, id))
}

func TestUnitOfWorkCommitsNestedAndRollsBackPanic(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	committed, panicked := uniqueID("nested"), uniqueID("panic")

	require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
		return store.Do(ctx, func(ctx context.Context) error { return store.Add(ctx, ordertest.New(t, committed)) })
	}))
	_, err := store.Get(ctx, committed)
	require.NoError(t, err)

	assert.Panics(t, func() {
		_ = store.Do(ctx, func(ctx context.Context) error {
			require.NoError(t, store.Add(ctx, ordertest.New(t, panicked)))
			panic("boom")
		})
	})
	_, err = store.Get(ctx, panicked)
	require.ErrorIs(t, err, order.ErrOrderNotFound)

	require.ErrorIs(t, store.Append(ctx, committed, 1, []order.DomainEvent{order.OrderCompleted{OrderID: committed}}),
		postgres.ErrOutboxOutsideTransaction)
}
