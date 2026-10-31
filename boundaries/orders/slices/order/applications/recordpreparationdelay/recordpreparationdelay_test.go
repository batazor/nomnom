package recordpreparationdelay_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/recordpreparationdelay"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func delay(eventID string, readyAt time.Time) recordpreparationdelay.Input {
	return recordpreparationdelay.Input{EventID: eventID, OrderID: "order-1", OccurredAt: occurredAt.Add(5 * time.Minute), ExpectedReadyAt: readyAt}
}

func TestDelayRequiresExpectedReadyTime(t *testing.T) {
	_, err := recordpreparationdelay.NewHandler(nil, nil, nil, NewMockNotifier(t)).Handle(context.Background(), delay("event-1", time.Time{}))

	require.ErrorIs(t, err, usecase.ErrInvalidInput)
}
