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

func TestOnlyCancelledAndCompletedAreFinal(t *testing.T) {
	final := map[State]bool{StatePlaced: false, StateConfirmed: false, StateCancelled: true, StateCompleted: true}
	require.Len(t, final, len(allStates))
	for _, state := range allStates {
		assert.Equal(t, final[state], state.IsFinal(), state)
	}
}
