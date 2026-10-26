package order

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order/vo"
)

func TestSnapshotRoundTripAndRestoreValidation(t *testing.T) {
	order := newTestOrder(t)
	_, err := order.RecordCourierAssignment(testNow)
	require.NoError(t, err)
	reason, err := NewCancellationReason(OutcomeFailure, "delivery_failed")
	require.NoError(t, err)
	_, err = order.Cancel(testNow, reason)
	require.NoError(t, err)

	snapshot := order.Snapshot()
	snapshot.Version = 3
	restored, err := Restore(snapshot)
	require.NoError(t, err)
	assert.Equal(t, snapshot, restored.Snapshot())
	assert.Equal(t, order.Total(), restored.Total())
	assert.Empty(t, restored.PullEvents())

	unknown := snapshot
	unknown.State = "lost"
	_, err = Restore(unknown)
	require.ErrorIs(t, err, ErrUnknownOrderState)

	duplicate := snapshot
	duplicate.Facts = []RecordedFact{{Fact: FactPickedUp, OccurredAt: testNow}, {Fact: FactPickedUp, OccurredAt: testNow}}
	_, err = Restore(duplicate)
	require.ErrorIs(t, err, ErrInvalidSnapshot)

	marketless := snapshot
	marketless.MarketID = vo.MarketID{}
	_, err = Restore(marketless)
	require.ErrorIs(t, err, ErrMarketRequired)

	foreign := snapshot
	foreign.Facts = []RecordedFact{{Fact: "payment_captured", OccurredAt: testNow}}
	_, err = Restore(foreign)
	require.ErrorIs(t, err, ErrInvalidSnapshot)
}
