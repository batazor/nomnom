package order

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order/vo"
)

func TestTransitionTableIsComplete(t *testing.T) {
	for _, state := range allStates {
		for _, trigger := range allTriggers {
			_, ok := transitionTable[transitionKey{from: state, trigger: trigger}]
			assert.True(t, ok, "missing outcome for state=%s trigger=%s", state, trigger)
		}
	}
	assert.Len(t, transitionTable, len(allStates)*len(allTriggers))
}

func TestOrderLifecycleUsesStrictTypedOutcomes(t *testing.T) {
	order := newTestOrder(t)

	confirmed, err := order.Confirm()
	require.NoError(t, err)
	assert.Equal(t, TransitionResult{From: StatePlaced, Trigger: TriggerConfirm, To: StateConfirmed}, confirmed)

	_, err = order.Confirm()
	var already AlreadyInState
	require.True(t, errors.As(err, &already))
	assert.Equal(t, StateConfirmed, already.State)

	completed, err := order.Complete()
	require.NoError(t, err)
	assert.Equal(t, StateCompleted, completed.To)

	_, err = order.Cancel(time.Now(), testReason(t))
	var invalid InvalidTransition
	require.True(t, errors.As(err, &invalid))
	assert.Equal(t, StateCompleted, invalid.From)
	assert.Equal(t, TriggerCancel, invalid.Trigger)
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
	assert.Equal(t, OrderCancelled{OrderID: "order-1", RequestedAt: requestedAt, Reason: reason}, events[0])
	assert.Empty(t, order.PullEvents())
	assert.Equal(t, reason, order.CancellationReason())
	assert.Equal(t, requestedAt, order.CancelledAt())

	_, err = order.Cancel(requestedAt, reason)
	var already AlreadyInState
	require.True(t, errors.As(err, &already))
	assert.Empty(t, order.PullEvents())
}

func TestOrderComputesTotalAndRejectsDuplicateItems(t *testing.T) {
	price, err := vo.ParseMoney("2.50", "EUR")
	require.NoError(t, err)
	quantity, err := vo.NewQuantity(2)
	require.NoError(t, err)
	address, err := vo.NewDeliveryAddress("Main street 1")
	require.NoError(t, err)
	item := NewItem("menu-1", quantity, price)

	order, err := New("order-1", "customer-1", "restaurant-1", []Item{item}, address)
	require.NoError(t, err)
	assert.Equal(t, "5 EUR", order.Total().String())

	_, err = New("order-2", "customer-1", "restaurant-1", []Item{item, item}, address)
	require.ErrorIs(t, err, ErrDuplicateMenuItem)
}

func newTestOrder(t *testing.T) *Order {
	t.Helper()
	price, err := vo.ParseMoney("10", "EUR")
	require.NoError(t, err)
	quantity, err := vo.NewQuantity(1)
	require.NoError(t, err)
	address, err := vo.NewDeliveryAddress("Main street 1")
	require.NoError(t, err)
	order, err := New(
		"order-1",
		"customer-1",
		"restaurant-1",
		[]Item{NewItem("menu-1", quantity, price)},
		address,
	)
	require.NoError(t, err)
	return order
}

func testReason(t *testing.T) CancellationReason {
	t.Helper()
	reason, err := NewCancellationReason(OutcomeCancellation, "customer_requested")
	require.NoError(t, err)
	return reason
}

func TestOnlyCancelledAndCompletedAreFinal(t *testing.T) {
	final := map[State]bool{StatePlaced: false, StateConfirmed: false, StateCancelled: true, StateCompleted: true}
	require.Len(t, final, len(allStates))
	for _, state := range allStates {
		assert.Equal(t, final[state], state.IsFinal(), state)
	}
}

func TestOnlyPlacedAndConfirmedAcceptCancellation(t *testing.T) {
	accepts := map[State]bool{StatePlaced: true, StateConfirmed: true, StateCancelled: false, StateCompleted: false}
	require.Len(t, accepts, len(allStates))
	for _, state := range allStates {
		assert.Equal(t, accepts[state], state.AcceptsCancellation(), state)
	}
}
