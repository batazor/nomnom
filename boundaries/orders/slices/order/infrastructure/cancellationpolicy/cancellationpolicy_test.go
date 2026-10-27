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
	policy, err := NewFixed(5 * time.Minute)
	require.NoError(t, err)
	placedAt := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)

	window, err := policy.Window(context.Background(), placedAt)

	require.NoError(t, err)
	assert.Equal(t, order.CancellationWindow{
		OpensAt:  placedAt,
		ClosesAt: placedAt.Add(5 * time.Minute),
	}, window)
}

func TestFixedRejectsNonPositiveWindow(t *testing.T) {
	for _, window := range []time.Duration{0, -time.Second} {
		_, err := NewFixed(window)
		assert.Error(t, err, window)
	}
}

// AC-507: the decorator logs a failure and passes it on unchanged.
func TestObservedLogsFailure(t *testing.T) {
	var logs bytes.Buffer
	placedAt := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	failing := NewMockPolicy(t)
	failing.EXPECT().
		Window(mock.Anything, placedAt).
		Return(order.CancellationWindow{}, errors.New("policy unavailable")).
		Once()
	policy := Observe(failing, slog.New(slog.NewTextHandler(&logs, nil)))

	_, err := policy.Window(context.Background(), placedAt)

	require.EqualError(t, err, "policy unavailable")
	assert.Contains(t, logs.String(), "cancellation window is unavailable")
	assert.Contains(t, logs.String(), "policy unavailable")
}
