package messaging_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/messaging"
)

func TestNewEventMessageCarriesInboxAndPartitionIdentity(t *testing.T) {
	msg, err := messaging.NewEventMessage("event-123", "order-456", []byte(`{"type":"OrderPlaced"}`))
	require.NoError(t, err)

	assert.Equal(t, "event-123", msg.UUID)
	assert.Equal(t, "event-123", msg.Metadata.Get(messaging.EventIDMetadata))
	assert.Equal(t, "order-456", msg.Metadata.Get(messaging.PartitionKeyMetadata))
	assert.JSONEq(t, `{"type":"OrderPlaced"}`, string(msg.Payload))
}

func TestNewEventMessageRequiresStableIdentities(t *testing.T) {
	_, err := messaging.NewEventMessage("", "order-456", nil)
	require.ErrorContains(t, err, "event id")

	_, err = messaging.NewEventMessage("event-123", "", nil)
	require.ErrorContains(t, err, "order id")
}
