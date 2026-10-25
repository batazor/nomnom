package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

// outboxRow is what Debezium turns into a Kafka message (ADR 37): topic, key
// aggregate_id, value payload and the Event Envelope headers.
type outboxRow struct {
	EventID          string
	EventType        string
	AggregateType    string
	AggregateID      string
	AggregateVersion int64
	OccurredAt       time.Time
	Topic            string
	Payload          []byte
	TraceParent      string
}

func outboxRows(t *testing.T, store *postgres.Store, id order.ID) []outboxRow {
	t.Helper()
	rows, err := store.Pool().Query(context.Background(), `
		SELECT event_id::text, event_type, aggregate_type, aggregate_id, aggregate_version,
			occurred_at, topic, payload, traceparent
		FROM orders_outbox WHERE aggregate_id = $1 ORDER BY seq`, string(id))
	require.NoError(t, err)
	defer rows.Close()
	var result []outboxRow
	for rows.Next() {
		var row outboxRow
		require.NoError(t, rows.Scan(&row.EventID, &row.EventType, &row.AggregateType, &row.AggregateID,
			&row.AggregateVersion, &row.OccurredAt, &row.Topic, &row.Payload, &row.TraceParent))
		result = append(result, row)
	}
	require.NoError(t, rows.Err())
	return result
}

func outboxCount(t *testing.T, store *postgres.Store, id order.ID) int {
	t.Helper()
	return len(outboxRows(t, store, id))
}

// AC-470, ADR 37: the committed OrderPlaced is one Outbox row with the full
// Event Envelope and the trace context of the write.
func TestOutboxStoresOrderPlacedWithEnvelope(t *testing.T) {
	store := open(t)
	id := uniqueID("cdc")
	placed := ordertest.New(t, id)
	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{2},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), span)
	require.NoError(t, store.Do(ctx, func(ctx context.Context) error {
		if err := store.Add(ctx, placed); err != nil {
			return err
		}
		return outbound.NewOutbox(store).Append(ctx, id, 1, []order.DomainEvent{order.OrderPlaced{
			OrderID: id, CustomerID: placed.CustomerID(), RestaurantID: placed.RestaurantID(),
			Total: placed.Total(), PlacedAt: placed.PlacedAt(), Items: placed.Items(),
		}})
	}))

	rows := outboxRows(t, store, id)
	require.Len(t, rows, 1)
	row := rows[0]
	assert.Equal(t, outbound.OrderEventsTopic, row.Topic)
	assert.Equal(t, "nomnom.orders.v1.OrderPlaced", row.EventType)
	assert.Equal(t, "order", row.AggregateType)
	assert.Equal(t, string(id), row.AggregateID)
	assert.Equal(t, int64(1), row.AggregateVersion)
	assert.Equal(t, outbound.EventID(id, 1, 0), row.EventID)
	assert.True(t, placed.PlacedAt().Equal(row.OccurredAt))
	assert.Equal(t, "00-01000000000000000000000000000000-0200000000000000-01", row.TraceParent)
	var event ordersv1.OrderPlaced
	require.NoError(t, proto.Unmarshal(row.Payload, &event))
	assert.Equal(t, string(id), event.GetOrderId())
	assert.Equal(t, "20", event.GetTotal().GetAmount())
	// restaurants creates its Restaurant Order from the composition.
	require.Len(t, event.GetItems(), len(placed.Items()))
	for index, item := range placed.Items() {
		assert.Equal(t, string(item.MenuItemID()), event.GetItems()[index].GetMenuItemId())
		assert.Equal(t, int32(item.Quantity().Value()), event.GetItems()[index].GetQuantity()) //nolint:gosec // small test quantities.
		assert.Equal(t, item.UnitPrice().Amount().String(), event.GetItems()[index].GetUnitPrice().GetAmount())
	}
}

// ADR 37: rows older than the retention go with the next Outbox write; the
// WAL already holds them, so retention never loses an event.
func TestOutboxRetentionDropsOldRows(t *testing.T) {
	store := open(t)
	stale, fresh := uniqueID("stale"), uniqueID("fresh")
	for _, id := range []order.ID{stale, fresh} {
		require.NoError(t, store.Do(context.Background(), func(ctx context.Context) error {
			return outbound.NewChangeNotifier(store).Notify(ctx, id, 1, time.Now())
		}))
	}
	_, err := store.Pool().Exec(context.Background(),
		`UPDATE orders_outbox SET created_at = now() - interval '2 hours' WHERE aggregate_id = $1`, string(stale))
	require.NoError(t, err)

	require.NoError(t, store.Do(context.Background(), func(ctx context.Context) error {
		return outbound.NewChangeNotifier(store).Notify(ctx, fresh, 2, time.Now())
	}))

	assert.Zero(t, outboxCount(t, store, stale))
	assert.Equal(t, 2, outboxCount(t, store, fresh))
}
