package order

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancellationReasonValidation(t *testing.T) {
	tests := []struct {
		name    string
		outcome NegativeOutcome
		code    string
		valid   bool
	}{
		{name: "cancellation", outcome: OutcomeCancellation, code: "customer_requested", valid: true},
		{name: "failure", outcome: OutcomeFailure, code: "restaurant_rejected", valid: true},
		{name: "unknown outcome", outcome: "refund", code: "x"},
		{name: "empty code", outcome: OutcomeFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, err := NewCancellationReason(tt.outcome, tt.code)
			if !tt.valid {
				require.ErrorIs(t, err, ErrInvalidCancellationReason)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.outcome, reason.Outcome())
			assert.Equal(t, tt.code, reason.Code())
		})
	}

	_, err := newTestOrder(t).Cancel(testNow, CancellationReason{})
	require.ErrorIs(t, err, ErrInvalidCancellationReason)
}

func TestCheckCancellationUsesWindowThenStateWithoutChangingOrder(t *testing.T) {
	window := CancellationWindow{OpensAt: testNow, ClosesAt: testNow.Add(10 * time.Minute)}

	tests := []struct {
		name        string
		state       State
		requestedAt time.Time
		wantErr     error
	}{
		{name: "placed inside window", state: StatePlaced, requestedAt: testNow.Add(time.Minute)},
		{name: "confirmed at deadline", state: StateConfirmed, requestedAt: window.ClosesAt},
		{
			name:        "before window",
			state:       StatePlaced,
			requestedAt: testNow.Add(-time.Second),
			wantErr:     CancellationNotAllowed{Reasons: []PolicyReason{{Code: CancellationWindowNotOpen}}},
		},
		{
			name:        "after window",
			state:       StateConfirmed,
			requestedAt: window.ClosesAt.Add(time.Nanosecond),
			wantErr:     CancellationNotAllowed{Reasons: []PolicyReason{{Code: CancellationWindowExpired}}},
		},
		{name: "already cancelled", state: StateCancelled, requestedAt: testNow, wantErr: AlreadyInState{State: StateCancelled}},
		{
			name:        "completed",
			state:       StateCompleted,
			requestedAt: testNow,
			wantErr:     InvalidTransition{From: StateCompleted, Trigger: TriggerCancel},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := orderInState(t, tt.state)

			err := order.CheckCancellation(tt.requestedAt, window)
			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				assert.Equal(t, tt.wantErr, err)
			}
			assert.Equal(t, tt.state, order.State())
			assert.Empty(t, order.PullEvents())
		})
	}
}

func TestCancelRequiresExplicitTimeAndRecordsOneDomainEvent(t *testing.T) {
	order := newTestOrder(t)

	reason := testReason(t)
	_, err := order.Cancel(time.Time{}, reason)
	var guard GuardFailed
	require.True(t, errors.As(err, &guard))
	assert.Equal(t, "requested_at_required", guard.Reason)
	assert.Equal(t, StatePlaced, order.State())
	assert.Empty(t, order.PullEvents())

	requestedAt := time.Date(2026, time.October, 24, 12, 0, 0, 0, time.UTC)
	_, err = order.Cancel(requestedAt, reason)
	require.NoError(t, err)

	events := order.PullEvents()
	require.Len(t, events, 1)
	assert.Equal(t, OrderCancelled{OrderID: "order-1", MarketID: testMarket(t), RequestedAt: requestedAt, Reason: reason}, events[0])
	assert.Empty(t, order.PullEvents())
	assert.Equal(t, reason, order.CancellationReason())
	assert.Equal(t, requestedAt, order.CancelledAt())

	_, err = order.Cancel(requestedAt, reason)
	var already AlreadyInState
	require.True(t, errors.As(err, &already))
	assert.Empty(t, order.PullEvents())
}

func testReason(t *testing.T) CancellationReason {
	t.Helper()
	reason, err := NewCancellationReason(OutcomeCancellation, "customer_requested")
	require.NoError(t, err)
	return reason
}

func TestOnlyOpenStatesAcceptCancellation(t *testing.T) {
	accepts := map[State]bool{
		StatePlaced: true, StatePaid: true, StateConfirmed: true, StatePreparing: true, StateReady: true, StatePickedUp: true,
		StateCancelled: false, StateCompleted: false,
	}
	require.Len(t, accepts, len(allStates))
	for _, state := range allStates {
		assert.Equal(t, accepts[state], state.AcceptsCancellation(), state)
	}
}
