package repository_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config/database"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/listforcustomer"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/domains/order/vo"
	"nomnom/orders/slices/order/infrastructure/repository"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

func TestOrderDetailsReads(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	customer := order.CustomerID(uniqueID("history"))
	var ids []order.ID
	for i := range 3 {
		id := uniqueID("list" + strconv.Itoa(i))
		ids = append(ids, id)
		require.NoError(t, store.Add(ctx, ordertest.New(t, id,
			ordertest.WithCustomer(customer), ordertest.WithPlacedAt(ordertest.PlacedAt.Add(time.Duration(i)*time.Minute)))))
	}

	details, err := store.OrderDetails(ctx, ids[0])
	require.NoError(t, err)
	assert.Equal(t, customer, details.CustomerID)
	assert.Equal(t, ordertest.MarketID(t), details.MarketID)
	require.Len(t, details.Items, 1)
	assert.Equal(t, "Margherita", details.Items[0].Name)
	assert.Nil(t, details.Cancellation)

	_, err = store.OrderDetails(ctx, "missing")
	require.ErrorIs(t, err, order.ErrOrderNotFound)

	first, err := store.ListCustomerOrders(ctx, listforcustomer.PageRequest{CustomerID: customer, MarketID: ordertest.MarketID(t), Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Rows, 2)
	assert.Equal(t, []order.ID{ids[2], ids[1]}, []order.ID{first.Rows[0].Details.OrderID, first.Rows[1].Details.OrderID})
	require.NotEmpty(t, first.NextCursor)
	assert.Equal(t, first.NextCursor, first.Rows[1].Cursor, "the last order continues the page")

	resumed, err := store.ListCustomerOrders(ctx, listforcustomer.PageRequest{CustomerID: customer, MarketID: ordertest.MarketID(t), Limit: 2, Cursor: first.Rows[0].Cursor})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{ids[1], ids[0]}, []order.ID{resumed.Rows[0].Details.OrderID, resumed.Rows[1].Details.OrderID}, "any order cursor resumes after it")

	second, err := store.ListCustomerOrders(ctx, listforcustomer.PageRequest{CustomerID: customer, MarketID: ordertest.MarketID(t), Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Rows, 1)
	assert.Equal(t, ids[0], second.Rows[0].Details.OrderID)
	assert.Empty(t, second.NextCursor)

	_, err = store.ListCustomerOrders(ctx, listforcustomer.PageRequest{CustomerID: customer, MarketID: ordertest.MarketID(t), Limit: 2, Cursor: "%%%"})
	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}

// ADR 41: the history is filtered by Market in SQL, with the keyset cursor.
func TestListCustomerOrdersFiltersByMarket(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	customer := order.CustomerID(uniqueID("markets"))
	berlin, hamburg := uniqueID("berlin"), uniqueID("hamburg")
	require.NoError(t, store.Add(ctx, ordertest.New(t, berlin, ordertest.WithCustomer(customer))))
	require.NoError(t, store.Add(ctx, ordertest.New(t, hamburg, ordertest.WithCustomer(customer), ordertest.WithMarket("hamburg"),
		ordertest.WithPlacedAt(ordertest.PlacedAt.Add(time.Minute)))))
	hamburgMarket, err := vo.NewMarketID("hamburg")
	require.NoError(t, err)

	page, err := store.ListCustomerOrders(ctx, listforcustomer.PageRequest{CustomerID: customer, MarketID: hamburgMarket, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	assert.Equal(t, hamburg, page.Rows[0].Details.OrderID)
	assert.Equal(t, hamburgMarket, page.Rows[0].Details.MarketID)

	page, err = store.ListCustomerOrders(ctx, listforcustomer.PageRequest{CustomerID: customer, MarketID: ordertest.MarketID(t), Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Rows, 1)
	assert.Equal(t, berlin, page.Rows[0].Details.OrderID)

	restored, err := store.Get(ctx, hamburg)
	require.NoError(t, err)
	assert.Equal(t, hamburgMarket, restored.MarketID(), "the Order keeps its Market in storage")
}

// Every change of the Order rewrites its row in the same transaction.
func TestOrderDetailsFollowOrderChanges(t *testing.T) {
	store := repositorytest.Open(t)
	ctx := context.Background()
	id := uniqueID("follow")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))

	require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
		current, err := store.Get(ctx, id)
		if err != nil {
			return err
		}
		if _, err := current.Confirm(ordertest.PlacedAt.Add(time.Minute)); err != nil {
			return err
		}
		return store.Save(ctx, current)
	}))

	details, err := store.OrderDetails(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, order.StateConfirmed, details.State)
	assert.Equal(t, []order.AppliedTransition{{Trigger: order.TriggerConfirm, OccurredAt: ordertest.PlacedAt.Add(time.Minute)}}, details.Transitions)
	assert.Equal(t, int64(2), projectedVersion(t, store, id), "the row reflects the saved Order version")
}

// Catch-up projects Orders without a row, with an older projection_version or
// of another Order version; rows that are current stay untouched.
func TestCatchUpProjectsStaleOrders(t *testing.T) {
	store := repositorytest.Open(t)
	ctx := context.Background()
	missing, outdated, drifted, current := uniqueID("missing"), uniqueID("outdated"), uniqueID("drifted"), uniqueID("current")
	for _, id := range []order.ID{missing, outdated, drifted, current} {
		require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	}
	exec(t, store, `DELETE FROM order_details WHERE order_id = $1`, missing)
	exec(t, store, `UPDATE order_details SET projection_version = projection_version - 1, details = '{}' WHERE order_id = $1`, outdated)
	exec(t, store, `UPDATE orders SET state = 'confirmed', version = version + 1 WHERE id = $1`, drifted)
	before := projectedAt(t, store, current)

	projected, err := store.CatchUpOrderDetails(ctx)

	require.NoError(t, err)
	assert.Equal(t, 3, projected)
	for _, id := range []order.ID{missing, outdated} {
		details, err := store.OrderDetails(ctx, id)
		require.NoError(t, err, id)
		assert.Equal(t, order.StatePlaced, details.State, id)
	}
	details, err := store.OrderDetails(ctx, drifted)
	require.NoError(t, err)
	assert.Equal(t, order.StateConfirmed, details.State, "a change written without the projection is caught up")
	assert.Equal(t, int64(2), projectedVersion(t, store, drifted))
	assert.Equal(t, before, projectedAt(t, store, current), "a current row is not projected again")

	again, err := store.CatchUpOrderDetails(ctx)
	require.NoError(t, err)
	assert.Zero(t, again, "nothing is stale after catch-up")
}

// A row of a newer projection_version belongs to the next release: a process of
// this one, restarted during a rollout, leaves it alone.
func TestCatchUpKeepsANewerProjectionVersion(t *testing.T) {
	store := repositorytest.Open(t)
	ctx := context.Background()
	id := uniqueID("newer")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	exec(t, store, `UPDATE order_details SET projection_version = projection_version + 1 WHERE order_id = $1`, id)

	projected, err := store.CatchUpOrderDetails(ctx)

	require.NoError(t, err)
	assert.Zero(t, projected)
}

// A pass waits for the holder of the lock and then does what is left; waiting
// ends with its context.
func TestCatchUpWaitsForTheLockHolder(t *testing.T) {
	store := repositorytest.Open(t)
	ctx := context.Background()
	id := uniqueID("locked")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	exec(t, store, `DELETE FROM order_details WHERE order_id = $1`, id)
	holder, err := store.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer holder.Release()
	const projectionLock int64 = 0x6f72645f64657461
	_, err = holder.Exec(ctx, `SELECT pg_advisory_lock($1)`, projectionLock)
	require.NoError(t, err)

	cancelled, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err = store.CatchUpOrderDetails(cancelled)
	require.Error(t, err)

	type result struct {
		projected int
		err       error
	}
	done := make(chan result, 1)
	go func() {
		projected, err := store.CatchUpOrderDetails(ctx)
		done <- result{projected, err}
	}()
	select {
	case <-done:
		t.Fatal("the pass did not wait for the lock")
	case <-time.After(100 * time.Millisecond):
	}

	_, err = holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, projectionLock)
	require.NoError(t, err)
	finished := <-done
	require.NoError(t, finished.err)
	assert.Equal(t, 1, finished.projected)
}

// The lock lives outside the pool, so the smallest valid pool still projects.
func TestCatchUpWorksOnAPoolOfOneConnection(t *testing.T) {
	store := repositorytest.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := uniqueID("single")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	exec(t, store, `DELETE FROM order_details WHERE order_id = $1`, id)
	single, err := repository.Open(ctx, database.Config{DSN: store.Pool().Config().ConnString(), MaxConns: 1})
	require.NoError(t, err)
	defer single.Close()

	projected, err := single.CatchUpOrderDetails(ctx)

	require.NoError(t, err)
	assert.Equal(t, 1, projected)
}

func exec(t *testing.T, store *repositorytest.Store, sql string, args ...any) {
	t.Helper()
	_, err := store.Pool().Exec(context.Background(), sql, args...)
	require.NoError(t, err)
}

func projectedVersion(t *testing.T, store *repositorytest.Store, id order.ID) int64 {
	t.Helper()
	var version int64
	require.NoError(t, store.Pool().QueryRow(context.Background(),
		`SELECT order_version FROM order_details WHERE order_id = $1`, id).Scan(&version))
	return version
}

func projectedAt(t *testing.T, store *repositorytest.Store, id order.ID) time.Time {
	t.Helper()
	var at time.Time
	require.NoError(t, store.Pool().QueryRow(context.Background(),
		`SELECT projected_at FROM order_details WHERE order_id = $1`, id).Scan(&at))
	return at
}
