package outbound_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
)

// The notification is the Cosmo Streams entity key only: Router resolves the
// Order itself, so no order data leaves orders through this topic.
func TestChangeNotifierStoresEntityKey(t *testing.T) {
	store := &recordingStore{}

	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	require.NoError(t, outbound.NewChangeNotifier(store).Notify(context.Background(), "order-1", 3, at))

	require.Len(t, store.messages, 1)
	assert.Equal(t, outbound.OrderChangesTopic, store.topic)
	assert.JSONEq(t, `{"__typename":"Order","id":"order-1"}`, string(store.messages[0].Payload))
	metadata := store.messages[0].Metadata
	assert.Equal(t, "order-1", metadata.Get(messaging.PartitionKeyMetadata))
	// ADR 37: every Outbox row carries the full Event Envelope.
	assert.Equal(t, store.messages[0].UUID, metadata.Get(messaging.EventIDMetadata))
	assert.Equal(t, "OrderChanged", metadata.Get(messaging.EventTypeMetadata))
	assert.Equal(t, "order", metadata.Get(messaging.AggregateTypeMetadata))
	assert.Equal(t, "order-1", metadata.Get(messaging.AggregateIDMetadata))
	assert.Equal(t, "3", metadata.Get(messaging.AggregateVersionMetadata))
	assert.Equal(t, "2026-10-25T12:00:00Z", metadata.Get(messaging.OccurredAtMetadata))
}
