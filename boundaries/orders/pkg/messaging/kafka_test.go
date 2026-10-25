package messaging_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/messaging"
)

func TestNewEventMessageRequiresStableIdentities(t *testing.T) {
	_, err := messaging.NewEventMessage("", "order-456", nil)
	require.ErrorContains(t, err, "event id")

	_, err = messaging.NewEventMessage("event-123", "", nil)
	require.ErrorContains(t, err, "order id")
}
