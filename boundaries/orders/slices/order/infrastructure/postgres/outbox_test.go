//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

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
