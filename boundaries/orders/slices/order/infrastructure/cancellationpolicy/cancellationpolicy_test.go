package cancellationpolicy

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order"
)

func TestFixedOpensAtPlacementForTheWindow(t *testing.T) {
	policy, err := NewFixed(5*time.Minute, time.Hour)
	require.NoError(t, err)
	placedAt := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)

	window, err := policy.Window(context.Background(), placedAt, order.DeliveryWindow{})

	require.NoError(t, err)
	assert.Equal(t, order.CancellationWindow{
		OpensAt:  placedAt,
		ClosesAt: placedAt.Add(5 * time.Minute),
	}, window)
}

func TestFixedRejectsNonPositiveWindow(t *testing.T) {
	for _, window := range []time.Duration{0, -time.Second} {
		_, err := NewFixed(window, time.Hour)
		assert.Error(t, err, window)
		_, err = NewFixed(5*time.Minute, window)
		assert.Error(t, err, window)
	}
}

// AC-916: window 19:00, cutoff 60 minutes: a request at 17:30 is inside the
// window, one at 18:10 after it.
func TestFixedClosesScheduledOrderBeforeItsWindow(t *testing.T) {
	policy, err := NewFixed(5*time.Minute, 60*time.Minute)
	require.NoError(t, err)
	placedAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	delivery, err := order.NewDeliveryWindow(time.Date(2026, 10, 25, 19, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 19, 15, 0, 0, time.UTC))
	require.NoError(t, err)

	window, err := policy.Window(context.Background(), placedAt, delivery)

	require.NoError(t, err)
	assert.Equal(t, order.CancellationWindow{OpensAt: placedAt, ClosesAt: time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)}, window)
	early := order.WithinCancellationWindow(order.CancellationWindowContext{
		Now: time.Date(2026, 10, 25, 17, 30, 0, 0, time.UTC), OpensAt: window.OpensAt, ClosesAt: window.ClosesAt,
	})
	assert.True(t, early.Satisfied())
	late := order.WithinCancellationWindow(order.CancellationWindowContext{
		Now: time.Date(2026, 10, 25, 18, 10, 0, 0, time.UTC), OpensAt: window.OpensAt, ClosesAt: window.ClosesAt,
	})
	assert.Equal(t, []order.PolicyReason{{Code: order.CancellationWindowExpired}}, late.Reasons)
}

// AC-507: the decorator logs a failure and passes it on unchanged.
func TestObservedLogsFailure(t *testing.T) {
	var logs bytes.Buffer
	placedAt := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	failing := NewMockPolicy(t)
	failing.EXPECT().
		Window(mock.Anything, placedAt, order.DeliveryWindow{}).
		Return(order.CancellationWindow{}, errors.New("policy unavailable")).
		Once()
	policy := Observe(failing, slog.New(slog.NewTextHandler(&logs, nil)))

	_, err := policy.Window(context.Background(), placedAt, order.DeliveryWindow{})

	require.EqualError(t, err, "policy unavailable")
	assert.Contains(t, logs.String(), "cancellation window is unavailable")
	assert.Contains(t, logs.String(), "policy unavailable")
}
