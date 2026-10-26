package repository_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
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
	require.Len(t, details.Items, 1)
	assert.Equal(t, "Margherita", details.Items[0].Name)
	assert.Nil(t, details.Cancellation)

	_, err = store.OrderDetails(ctx, "missing")
	require.ErrorIs(t, err, order.ErrOrderNotFound)

	first, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Rows, 2)
	assert.Equal(t, []order.ID{ids[2], ids[1]}, []order.ID{first.Rows[0].Details.OrderID, first.Rows[1].Details.OrderID})
	require.NotEmpty(t, first.NextCursor)
	assert.Equal(t, first.NextCursor, first.Rows[1].Cursor, "the last order continues the page")

	resumed, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2, Cursor: first.Rows[0].Cursor})
	require.NoError(t, err)
	assert.Equal(t, []order.ID{ids[1], ids[0]}, []order.ID{resumed.Rows[0].Details.OrderID, resumed.Rows[1].Details.OrderID}, "any order cursor resumes after it")

	second, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Rows, 1)
	assert.Equal(t, ids[0], second.Rows[0].Details.OrderID)
	assert.Empty(t, second.NextCursor)

	_, err = store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2, Cursor: "%%%"})
	require.ErrorIs(t, err, application.ErrInvalidInput)
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
	assert.Equal(t, []orderdetails.Fact{{Fact: order.FactRestaurantAccepted, OccurredAt: ordertest.PlacedAt.Add(time.Minute)}}, details.Facts)
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
	exec(t, store, `UPDATE order_details SET projection_version = projection_version + 1000, details = '{}' WHERE order_id = $1`, outdated)
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

// Rebuild projects every Order again, current rows included.
func TestRebuildProjectsEveryOrder(t *testing.T) {
	store := repositorytest.Open(t)
	ctx := context.Background()
	id := uniqueID("rebuild")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	exec(t, store, `UPDATE order_details SET details = jsonb_set(details, '{state}', '"broken"') WHERE order_id = $1`, id)

	projected, err := store.ReprojectOrderDetails(ctx, repository.ScopeAll)

	require.NoError(t, err)
	assert.Equal(t, 1, projected)
	details, err := store.OrderDetails(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, order.StatePlaced, details.State)

	_, err = store.ReprojectOrderDetails(ctx, "partial")
	require.Error(t, err, "an unknown scope is an error, not a default")
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
