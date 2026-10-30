package fulfillment_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
)

var occurredAt = time.Date(2026, time.October, 25, 13, 0, 0, 0, time.UTC)

func TestClassifySignalOfAppliedStep(t *testing.T) {
	current := ordertest.New(t, "order-1")
	_, err := current.RecordPickup(occurredAt)

	outcome, err := fulfillment.ClassifySignal(current, err)

	require.NoError(t, err)
	assert.Equal(t, fulfillment.OutcomeApplied, outcome)
}

// AC-107: the same step under a new event_id has no second business effect.
func TestClassifySignalOfRepeatedStep(t *testing.T) {
	t.Run("order is already in the state", func(t *testing.T) {
		current := ordertest.New(t, "order-1", ordertest.WithState(order.StateConfirmed))
		_, err := current.Confirm(occurredAt)

		outcome, err := fulfillment.ClassifySignal(current, err)

		require.NoError(t, err)
		assert.Equal(t, fulfillment.OutcomeAlreadyApplied, outcome)
	})
	t.Run("transition is already applied", func(t *testing.T) {
		recorded := order.AppliedTransition{Trigger: order.TriggerPickUp, OccurredAt: occurredAt}
		current := ordertest.New(t, "order-1", ordertest.WithTransitions(recorded))
		_, err := current.RecordPickup(occurredAt)

		outcome, err := fulfillment.ClassifySignal(current, err)

		require.NoError(t, err)
		assert.Equal(t, fulfillment.OutcomeAlreadyApplied, outcome)
	})
}

// AC-112: a signal for a final Order is late.
func TestClassifySignalForFinalOrderIsLate(t *testing.T) {
	for _, state := range []order.State{order.StateCompleted, order.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			current := ordertest.New(t, "order-1", ordertest.WithState(state))
			_, err := current.RecordPickup(occurredAt)

			outcome, err := fulfillment.ClassifySignal(current, err)

			require.NoError(t, err)
			assert.Equal(t, fulfillment.OutcomeIgnoredLate, outcome)
		})
	}
}

func TestClassifySignalReturnsOtherErrors(t *testing.T) {
	t.Run("invalid transition of a non-final order is a defect", func(t *testing.T) {
		current := ordertest.New(t, "order-1")
		_, err := current.Complete(occurredAt)

		_, err = fulfillment.ClassifySignal(current, err)

		var invalid order.InvalidTransition
		require.ErrorAs(t, err, &invalid)
	})
	t.Run("unknown error", func(t *testing.T) {
		failure := errors.New("disk full")

		_, err := fulfillment.ClassifySignal(ordertest.New(t, "order-1"), failure)

		require.ErrorIs(t, err, failure)
	})
}
