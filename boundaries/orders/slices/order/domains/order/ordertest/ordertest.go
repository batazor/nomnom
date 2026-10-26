// Package ordertest provides named Order fixtures for unit and acceptance tests.
package ordertest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// PlacedAt is the default placement time of fixtures.
var PlacedAt = time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)

type Option func(*order.Snapshot)

func WithState(state order.State) Option {
	return func(snapshot *order.Snapshot) { snapshot.State = state }
}

func WithCustomer(customerID order.CustomerID) Option {
	return func(snapshot *order.Snapshot) { snapshot.CustomerID = customerID }
}

func WithPlacedAt(placedAt time.Time) Option {
	return func(snapshot *order.Snapshot) { snapshot.PlacedAt = placedAt }
}

func WithFacts(facts ...order.RecordedFact) Option {
	return func(snapshot *order.Snapshot) { snapshot.Facts = facts }
}

func WithExpectedTimes(expected order.ExpectedTimes) Option {
	return func(snapshot *order.Snapshot) { snapshot.Expected = expected }
}

// New returns a standard Order with one item "menu-1" × 2 at 10 EUR for
// customer-1 in restaurant-1, not yet persisted (version 0).
func New(t testing.TB, id order.ID, options ...Option) *order.Order {
	t.Helper()
	snapshot := order.Snapshot{
		ID:           id,
		CustomerID:   "customer-1",
		RestaurantID: "restaurant-1",
		Items:        []order.Item{order.NewItem("menu-1", "Margherita", Quantity(t, 2), Money(t, "10"))},
		PlacedAt:     PlacedAt,
		State:        order.StatePlaced,
	}
	for _, option := range options {
		option(&snapshot)
	}
	if snapshot.State == order.StateCancelled && snapshot.CancellationReason.IsZero() {
		reason, err := order.NewCancellationReason(order.OutcomeCancellation, "customer_requested")
		require.NoError(t, err)
		snapshot.CancellationReason = reason
		snapshot.CancelledAt = snapshot.PlacedAt
	}
	built, err := order.Restore(snapshot)
	require.NoError(t, err)
	return built
}

func Money(t testing.TB, amount string) vo.Money {
	t.Helper()
	money, err := vo.ParseMoney(amount, "EUR")
	require.NoError(t, err)
	return money
}

func Quantity(t testing.TB, value int) vo.Quantity {
	t.Helper()
	quantity, err := vo.NewQuantity(value)
	require.NoError(t, err)
	return quantity
}
