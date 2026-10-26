package order

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order/vo"
)

// TriggerOf relies on every allowed transition of a State having its own target.
func TestTriggerOfNamesEveryAllowedTransition(t *testing.T) {
	for _, from := range allStates {
		for _, trigger := range allTriggers {
			row, err := resolve(from, trigger)
			if err != nil {
				continue
			}
			to := row.targetOf(from)
			named, ok := TriggerOf(from, to)
			require.True(t, ok, "from=%s to=%s", from, to)
			assert.Equal(t, trigger, named, "from=%s to=%s", from, to)
		}
	}
	_, ok := TriggerOf(StateCompleted, StatePlaced)
	assert.False(t, ok)
}

func TestOrderLifecycleUsesStrictTypedOutcomes(t *testing.T) {
	order := newTestOrder(t)

	confirmed, err := order.Confirm(testNow)
	require.NoError(t, err)
	assert.Equal(t, TransitionResult{From: StatePlaced, Trigger: TriggerConfirm, To: StateConfirmed}, confirmed)

	_, err = order.Confirm(testNow)
	var already AlreadyInState
	require.True(t, errors.As(err, &already))
	assert.Equal(t, StateConfirmed, already.State)

	completed, err := order.Complete(testNow)
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
	item := NewItem("menu-1", "Margherita", quantity, price)

	order, err := New("order-1", "customer-1", "restaurant-1", []Item{item})
	require.NoError(t, err)
	assert.Equal(t, "5 EUR", order.Total().String())

	_, err = New("order-2", "customer-1", "restaurant-1", []Item{item, item})
	require.ErrorIs(t, err, ErrDuplicateMenuItem)
}

func newTestOrder(t *testing.T) *Order {
	t.Helper()
	price, err := vo.ParseMoney("10", "EUR")
	require.NoError(t, err)
	quantity, err := vo.NewQuantity(1)
	require.NoError(t, err)
	order, err := New(
		"order-1",
		"customer-1",
		"restaurant-1",
		[]Item{NewItem("menu-1", "Margherita", quantity, price)},
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

// AC-563: confirmation keeps the moment of Order Acceptance as a fact.
func TestConfirmRecordsRestaurantAcceptance(t *testing.T) {
	order := newTestOrder(t)
	acceptedAt := testNow.Add(3 * time.Minute)

	_, err := order.Confirm(acceptedAt)
	require.NoError(t, err)
	assert.Equal(t, []RecordedFact{{Fact: FactRestaurantAccepted, OccurredAt: acceptedAt}}, order.Facts())

	_, err = order.Confirm(acceptedAt.Add(time.Minute))
	require.ErrorAs(t, err, new(AlreadyInState))
	assert.Len(t, order.Facts(), 1, "a repeated confirmation records nothing")

	_, err = newTestOrder(t).Confirm(time.Time{})
	assert.Equal(t, GuardFailed{Reason: "occurred_at_required"}, err)
}
