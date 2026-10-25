package outbound_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/messaging"
	"nomnom/orders/slices/order/infrastructure/messaging/outbound"
)

// The notification is the Cosmo Streams entity key only: Router resolves the
// Order itself, so no order data leaves orders through this topic.
func TestChangeNotifierStoresEntityKey(t *testing.T) {
	store := &recordingStore{}

	require.NoError(t, outbound.NewChangeNotifier(store).Notify(context.Background(), "order-1"))

	require.Len(t, store.messages, 1)
	assert.Equal(t, outbound.OrderChangesTopic, store.topic)
	assert.JSONEq(t, `{"__typename":"Order","id":"order-1"}`, string(store.messages[0].Payload))
	assert.Equal(t, "order-1", store.messages[0].Metadata.Get(messaging.PartitionKeyMetadata))
}
