package order

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order/vo"
)

// TriggerOf relies on every transition that moves a State having its own target;
// a change that keeps the State is named update_details.
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
			if to == from {
				trigger = TriggerUpdateDetails
			}
			assert.Equal(t, trigger, named, "from=%s to=%s", from, to)
		}
	}
	_, ok := TriggerOf(StateCompleted, StatePlaced)
	assert.False(t, ok)
	_, ok = TriggerOf(StatePreparing, StateConfirmed)
	assert.False(t, ok, "an Order never moves back")
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

	_, err = order.RecordDeliveryCompletion(testNow)
	require.NoError(t, err)
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

	order, err := New("order-1", "customer-1", "restaurant-1", testMarket(t), []Item{item}, StandardService())
	require.NoError(t, err)
	assert.Equal(t, "5 EUR", order.Total().String())

	_, err = New("order-2", "customer-1", "restaurant-1", testMarket(t), []Item{item, item}, StandardService())
	require.ErrorIs(t, err, ErrDuplicateMenuItem)
}

// ADR 41: the Market is required; there is no Market by default.
func TestOrderRequiresMarket(t *testing.T) {
	price, err := vo.ParseMoney("2.50", "EUR")
	require.NoError(t, err)
	quantity, err := vo.NewQuantity(1)
	require.NoError(t, err)

	_, err = New("order-1", "customer-1", "restaurant-1", vo.MarketID{}, []Item{NewItem("menu-1", "Margherita", quantity, price)}, StandardService())
	require.ErrorIs(t, err, ErrMarketRequired)
	require.ErrorIs(t, err, vo.ErrInvalidValue)

	order, err := New("order-1", "customer-1", "restaurant-1", testMarket(t), []Item{NewItem("menu-1", "Margherita", quantity, price)}, StandardService())
	require.NoError(t, err)
	assert.Equal(t, "berlin", order.MarketID().String())
}

// AC-854: the premium fee is part of the total.
func TestPremiumOrderAddsTheFeeToTheTotal(t *testing.T) {
	price, err := vo.ParseMoney("250.00", "RUB")
	require.NoError(t, err)
	quantity, err := vo.NewQuantity(2)
	require.NoError(t, err)
	fee, err := vo.ParseMoney("99.00", "RUB")
	require.NoError(t, err)
	service, err := PremiumService(fee)
	require.NoError(t, err)

	order, err := New("order-1", "customer-1", "restaurant-1", testMarket(t), []Item{NewItem("menu-1", "Pho", quantity, price)}, service)

	require.NoError(t, err)
	assert.Equal(t, "599 RUB", order.Total().String())
	assert.Equal(t, PriorityPremium, order.Service().Priority())
	stored, ok := order.Service().PremiumFee()
	require.True(t, ok)
	assert.True(t, stored.Equal(fee))
}

// FR-650: the fee is in the currency of the items and positive; a standard
// Order has none.
func TestPremiumServiceRules(t *testing.T) {
	price, err := vo.ParseMoney("10", "EUR")
	require.NoError(t, err)
	quantity, err := vo.NewQuantity(1)
	require.NoError(t, err)
	items := []Item{NewItem("menu-1", "Pho", quantity, price)}

	zero, err := vo.ZeroMoney("EUR")
	require.NoError(t, err)
	_, err = PremiumService(zero)
	require.ErrorIs(t, err, ErrInvalidPremiumFee)

	rub, err := vo.ParseMoney("99", "RUB")
	require.NoError(t, err)
	service, err := PremiumService(rub)
	require.NoError(t, err)
	_, err = New("order-1", "customer-1", "restaurant-1", testMarket(t), items, service)
	require.ErrorIs(t, err, vo.ErrCurrencyMismatch)

	_, err = New("order-1", "customer-1", "restaurant-1", testMarket(t), items, Service{})
	require.ErrorIs(t, err, ErrUnknownPriority)

	_, err = RestoreService(PriorityStandard, &rub)
	require.ErrorIs(t, err, ErrInvalidPremiumFee)
	_, err = RestoreService(PriorityPremium, nil)
	require.ErrorIs(t, err, ErrInvalidPremiumFee)
	_, err = ParsePriority("")
	require.ErrorIs(t, err, ErrUnknownPriority)
}

func testMarket(t testing.TB) vo.MarketID {
	t.Helper()
	market, err := vo.NewMarketID("berlin")
	require.NoError(t, err)
	return market
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
		testMarket(t),
		[]Item{NewItem("menu-1", "Margherita", quantity, price)},
		StandardService(),
	)
	require.NoError(t, err)
	return order
}

func TestOnlyCancelledAndCompletedAreFinal(t *testing.T) {
	final := map[State]bool{
		StatePlaced: false, StatePaid: false, StateConfirmed: false, StatePreparing: false, StateReady: false, StatePickedUp: false,
		StateRejected: false, StateDeliveryFailed: false, StateDelivered: false,
		StateCancelled: true, StateCompleted: true,
	}
	require.Len(t, final, len(allStates))
	for _, state := range allStates {
		assert.Equal(t, final[state], state.IsFinal(), state)
	}
}

// AC-563: confirmation keeps the moment of Order Acceptance in the history.
func TestConfirmRecordsRestaurantAcceptance(t *testing.T) {
	order := newTestOrder(t)
	acceptedAt := testNow.Add(3 * time.Minute)

	_, err := order.Confirm(acceptedAt)
	require.NoError(t, err)
	assert.Equal(t, []AppliedTransition{{Trigger: TriggerConfirm, OccurredAt: acceptedAt}}, order.Transitions())

	_, err = order.Confirm(acceptedAt.Add(time.Minute))
	require.ErrorAs(t, err, new(AlreadyInState))
	assert.Len(t, order.Transitions(), 1, "a repeated confirmation records nothing")

	_, err = newTestOrder(t).Confirm(time.Time{})
	assert.Equal(t, GuardFailed{Reason: "occurred_at_required"}, err)
}
