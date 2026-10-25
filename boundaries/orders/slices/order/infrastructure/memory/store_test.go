package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/applications/requestordercancellation"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/memory"
)

var (
	_ application.UnitOfWork                      = (*memory.Store)(nil)
	_ application.Inbox                           = (*memory.Store)(nil)
	_ order.Repository                            = (*memory.Store)(nil)
	_ placeorder.Outbox                           = (*memory.Store)(nil)
	_ completeorder.Outbox                        = (*memory.Store)(nil)
	_ placeorder.IdempotencyKeys                  = (*memory.Store)(nil)
	_ getorder.Reader                             = (*memory.Store)(nil)
	_ listcustomerorders.Reader                   = (*memory.Store)(nil)
	_ placeorder.Catalog                          = (*memory.Catalog)(nil)
	_ placeorder.Relationships                    = (*memory.Authorizer)(nil)
	_ getorder.Authorizer                         = (*memory.Authorizer)(nil)
	_ placeorder.IDGenerator                      = (*memory.IDs)(nil)
	_ application.Clock                           = (*memory.Clock)(nil)
	_ fulfillment.Starter                         = (*memory.Workflow)(nil)
	_ fulfillment.Notifier                        = (*memory.Workflow)(nil)
	_ fulfillment.CancellationRequester           = (*memory.Workflow)(nil)
	_ requestordercancellation.CancellationPolicy = memory.AlwaysOpen{}
	_ requestordercancellation.CancellationPolicy = memory.FixedWindow{}
)

func TestUnitOfWorkCommitsOrRollsBackEveryRecord(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")

	tests := []struct {
		name      string
		fail      error
		panics    bool
		committed bool
	}{
		{name: "commit", committed: true},
		{name: "error rolls back", fail: boom},
		{name: "panic rolls back", panics: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := memory.NewStore()
			work := func(ctx context.Context) error {
				require.NoError(t, store.Add(ctx, ordertest.New(t, "order-1")))
				_, err := store.Accept(ctx, "orders.test", "event-1")
				require.NoError(t, err)
				require.NoError(t, store.Append(ctx, "order-1", 1, []order.DomainEvent{order.OrderCompleted{OrderID: "order-1"}}))
				require.NoError(t, store.RememberOrder(ctx, "customer-1", "key-1", "order-1"))
				if tt.panics {
					panic("boom")
				}
				return tt.fail
			}

			var err error
			if tt.panics {
				assert.PanicsWithValue(t, "boom", func() { _ = store.Do(ctx, work) })
			} else {
				err = store.Do(ctx, work)
			}

			_, exists := store.Snapshot("order-1")
			_, remembered, findErr := store.FindOrder(ctx, "customer-1", "key-1")
			require.NoError(t, findErr)
			if tt.committed {
				require.NoError(t, err)
				assert.True(t, exists)
				assert.True(t, remembered)
				assert.Equal(t, 1, store.InboxSize())
				assert.Len(t, store.OutboxRecords(), 1)
				assert.Equal(t, 1, store.Commits())
				return
			}
			if tt.fail != nil {
				require.ErrorIs(t, err, boom)
			}
			assert.False(t, exists)
			assert.False(t, remembered)
			assert.Zero(t, store.InboxSize())
			assert.Empty(t, store.OutboxRecords())
			assert.Equal(t, 1, store.Rollbacks())
		})
	}
}

func TestNestedDoJoinsOuterTransaction(t *testing.T) {
	store := memory.NewStore()
	boom := errors.New("outer failure")

	err := store.Do(context.Background(), func(ctx context.Context) error {
		require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
			return store.Add(ctx, ordertest.New(t, "order-1"))
		}))
		return boom
	})

	require.ErrorIs(t, err, boom)
	_, exists := store.Snapshot("order-1")
	assert.False(t, exists, "inner scope must not commit on its own")
	assert.Zero(t, store.Commits())
	assert.Equal(t, 1, store.Rollbacks())
}

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

func TestListCustomerOrdersPaginatesNewestFirst(t *testing.T) {
	ctx := context.Background()
	store := memory.NewStore()
	for i, id := range []order.ID{"order-a", "order-b", "order-c"} {
		placedAt := ordertest.PlacedAt.Add(time.Duration(i) * time.Minute)
		require.NoError(t, store.Add(ctx, ordertest.New(t, id, ordertest.WithPlacedAt(placedAt))))
	}
	require.NoError(t, store.Add(ctx, ordertest.New(t, "foreign", ordertest.WithCustomer("customer-2"))))

	first, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: "customer-1", Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{"order-c", "order-b"}, ids(first))
	require.NotEmpty(t, first.NextCursor)

	second, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: "customer-1", Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{"order-a"}, ids(second))
	assert.Empty(t, second.NextCursor)
	assert.Equal(t, "20 EUR", second.Orders[0].Total.String())

	_, err = store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: "customer-1", Limit: 2, Cursor: "x"})
	require.ErrorIs(t, err, application.ErrInvalidInput)
}

func ids(page listcustomerorders.Page) []order.ID {
	result := make([]order.ID, 0, len(page.Orders))
	for _, summary := range page.Orders {
		result = append(result, summary.OrderID)
	}
	return result
}
