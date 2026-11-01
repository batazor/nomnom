package order_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
)

func TestExpectedTimesReplaceNamedOnes(t *testing.T) {
	o := ordertest.New(t, "order-1")
	at := ordertest.PlacedAt.Add(time.Minute)

	changed, err := o.ExpectReadyAt(at, at.Add(10*time.Minute), 1)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = o.ExpectReadyAt(at, at.Add(10*time.Minute), 1)
	require.NoError(t, err)
	assert.False(t, changed, "the same time changes nothing")
	_, err = o.ExpectPickupAt(at, at.Add(15*time.Minute))
	require.NoError(t, err)
	_, err = o.ExpectDeliveredAt(at, at.Add(30*time.Minute))
	require.NoError(t, err)
	changed, err = o.ExpectPickupAt(at, time.Time{})
	require.NoError(t, err)
	assert.True(t, changed, "a courier without a named time clears it")

	assert.Equal(t, order.ExpectedTimes{ReadyAt: at.Add(10 * time.Minute), ReadyVersion: 1, DeliveredAt: at.Add(30 * time.Minute)}, o.ExpectedTimes())
}

// A redriven event of an older Restaurant Order version keeps the newer time.
func TestOlderVersionDoesNotReplaceExpectedReadyTime(t *testing.T) {
	o := ordertest.New(t, "order-1")
	at := ordertest.PlacedAt
	newer := at.Add(20 * time.Minute)

	_, err := o.ExpectReadyAt(at, newer, 5)
	require.NoError(t, err)
	changed, err := o.ExpectReadyAt(at, at.Add(10*time.Minute), 4)
	require.NoError(t, err)

	assert.False(t, changed)
	assert.Equal(t, order.ExpectedTimes{ReadyAt: newer, ReadyVersion: 5}, o.ExpectedTimes())
}

func TestFinalOrderTakesNoExpectedTimes(t *testing.T) {
	for _, state := range []order.State{order.StateCompleted, order.StateCancelled} {
		o := ordertest.New(t, "order-1", ordertest.WithState(state))

		_, err := o.ExpectReadyAt(ordertest.PlacedAt, ordertest.PlacedAt.Add(time.Hour), 1)

		var invalid order.InvalidTransition
		require.ErrorAs(t, err, &invalid)
		assert.Zero(t, o.ExpectedTimes())
	}
}

func TestSnapshotKeepsExpectedTimes(t *testing.T) {
	expected := order.ExpectedTimes{ReadyAt: ordertest.PlacedAt.Add(time.Hour)}
	o := ordertest.New(t, "order-1", ordertest.WithExpectedTimes(expected))

	restored, err := order.Restore(o.Snapshot())

	require.NoError(t, err)
	assert.Equal(t, expected, restored.ExpectedTimes())
}
