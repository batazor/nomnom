//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/applications/listcustomerorders"
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

func TestRepositoryRoundTrip(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("rt")

	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	loaded, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), loaded.Version())
	assert.Equal(t, order.StatePlaced, loaded.State())
	assert.Equal(t, "20", loaded.Total().Amount().String())

	_, err = loaded.Confirm()
	require.NoError(t, err)
	_, err = loaded.RecordPreparationStarted(ordertest.PlacedAt.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, loaded))

	saved, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), saved.Version())
	assert.Equal(t, order.StateConfirmed, saved.State())
	require.Len(t, saved.Facts(), 1)
	assert.Equal(t, order.FactPreparationStarted, saved.Facts()[0].Fact)
	assert.Equal(t, ordertest.New(t, id).Items(), saved.Items())
}

func TestRepositoryKeepsCancellation(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("cancel")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))

	loaded, err := store.Get(ctx, id)
	require.NoError(t, err)
	reason, err := order.NewCancellationReason(order.OutcomeFailure, "delivery_failed")
	require.NoError(t, err)
	cancelledAt := ordertest.PlacedAt.Add(time.Hour)
	_, err = loaded.Cancel(cancelledAt, reason)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, loaded))

	saved, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, order.StateCancelled, saved.State())
	assert.Equal(t, reason, saved.CancellationReason())
	assert.Equal(t, cancelledAt, saved.CancelledAt())
}

// AC-468: the second writer of the same version gets a concurrency conflict.
func TestRepositoryOptimisticConcurrency(t *testing.T) {
	store := open(t)
	ctx := context.Background()
	id := uniqueID("cc")
	require.NoError(t, store.Add(ctx, ordertest.New(t, id)))
	require.ErrorIs(t, store.Add(ctx, ordertest.New(t, id)), order.ErrOrderAlreadyExists)

	first, err := store.Get(ctx, id)
	require.NoError(t, err)
	second, err := store.Get(ctx, id)
	require.NoError(t, err)
	_, err = first.Confirm()
	require.NoError(t, err)
	_, err = second.Confirm()
	require.NoError(t, err)

	require.NoError(t, store.Save(ctx, first))
	require.ErrorIs(t, store.Save(ctx, second), application.ErrConcurrencyConflict)
	require.ErrorIs(t, store.Save(ctx, ordertest.New(t, uniqueID("missing"))), order.ErrOrderNotFound)
	_, err = store.Get(ctx, uniqueID("missing"))
	require.ErrorIs(t, err, order.ErrOrderNotFound)
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

func TestReadModels(t *testing.T) {
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

	view, err := store.OrderView(ctx, ids[0])
	require.NoError(t, err)
	assert.Equal(t, customer, view.CustomerID)
	require.Len(t, view.Items, 1)
	assert.Nil(t, view.Cancellation)

	first, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2})
	require.NoError(t, err)
	require.Len(t, first.Orders, 2)
	assert.Equal(t, []order.ID{ids[2], ids[1]}, []order.ID{first.Orders[0].OrderID, first.Orders[1].OrderID})
	require.NotEmpty(t, first.NextCursor)

	second, err := store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Orders, 1)
	assert.Equal(t, ids[0], second.Orders[0].OrderID)
	assert.Empty(t, second.NextCursor)

	_, err = store.ListCustomerOrders(ctx, listcustomerorders.PageRequest{CustomerID: customer, Limit: 2, Cursor: "%%%"})
	require.ErrorIs(t, err, application.ErrInvalidInput)
}

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

// AC-470: the Forwarder publishes the committed OrderPlaced with its envelope.
func TestOutboxForwarderPublishesOrderPlaced(t *testing.T) {
	store := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id := uniqueID("fwd")
	placed := ordertest.New(t, id)
	require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
		if err := store.Add(ctx, placed); err != nil {
			return err
		}
		return store.Append(ctx, id, 1, []order.DomainEvent{order.OrderPlaced{
			OrderID: id, CustomerID: placed.CustomerID(), RestaurantID: placed.RestaurantID(),
			Total: placed.Total(), PlacedAt: placed.PlacedAt(),
		}})
	}))

	kafka := gochannel.NewGoChannel(gochannel.Config{}, watermill.NopLogger{})
	messages, err := kafka.Subscribe(ctx, postgres.OrderEventsTopic)
	require.NoError(t, err)
	fwd, err := postgres.NewForwarder(store, kafka, watermill.NopLogger{})
	require.NoError(t, err)
	go func() { _ = fwd.Run(ctx) }()
	t.Cleanup(func() { _ = fwd.Close() })

	for {
		select {
		case <-ctx.Done():
			t.Fatal("OrderPlaced was not forwarded")
		case msg := <-messages:
			msg.Ack()
			if msg.Metadata.Get(messaging.AggregateIDMetadata) != string(id) {
				continue
			}
			assert.Equal(t, "nomnom.orders.v1.OrderPlaced", msg.Metadata.Get(messaging.EventTypeMetadata))
			assert.Equal(t, string(id), msg.Metadata.Get(messaging.PartitionKeyMetadata))
			assert.Equal(t, msg.UUID, msg.Metadata.Get(messaging.EventIDMetadata))
			var event ordersv1.OrderPlaced
			require.NoError(t, proto.Unmarshal(msg.Payload, &event))
			assert.Equal(t, string(id), event.GetOrderId())
			assert.Equal(t, "20", event.GetTotal().GetAmount())
			return
		}
	}
}

func outboxCount(t *testing.T, store *postgres.Store, id order.ID) int {
	t.Helper()
	var count int
	require.NoError(t, store.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM watermill_orders_outbox WHERE payload::text LIKE '%' || $1 || '%'`, string(id)).Scan(&count))
	return count
}
