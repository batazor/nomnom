package order

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order/vo"
)

func TestWithinCancellationWindowIncludesBoundaries(t *testing.T) {
	opensAt := time.Date(2026, time.October, 24, 10, 0, 0, 0, time.UTC)
	closesAt := opensAt.Add(30 * time.Minute)

	assert.True(t, WithinCancellationWindow(CancellationWindowContext{
		Now: opensAt, OpensAt: opensAt, ClosesAt: closesAt,
	}).Satisfied())
	assert.True(t, WithinCancellationWindow(CancellationWindowContext{
		Now: closesAt, OpensAt: opensAt, ClosesAt: closesAt,
	}).Satisfied())

	tooEarly := WithinCancellationWindow(CancellationWindowContext{
		Now: opensAt.Add(-time.Nanosecond), OpensAt: opensAt, ClosesAt: closesAt,
	})
	assert.Equal(t, []PolicyReason{{Code: CancellationWindowNotOpen}}, tooEarly.Reasons)

	expired := WithinCancellationWindow(CancellationWindowContext{
		Now: closesAt.Add(time.Nanosecond), OpensAt: opensAt, ClosesAt: closesAt,
	})
	assert.Equal(t, []PolicyReason{{Code: CancellationWindowExpired}}, expired.Reasons)
}

func TestOrderItemsOrderableCollectsAvailabilityAndAmountReasons(t *testing.T) {
	order := newTestOrder(t)

	result := OrderItemsOrderable(order, OrderabilityContext{
		Availability: ItemAvailability{},
		MinimumTotal: mustPolicyMoney(t, "11"),
		MaximumTotal: mustPolicyMoney(t, "20"),
	})

	assert.False(t, result.Satisfied())
	assert.Equal(t, []PolicyReason{
		{Code: OrderItemUnavailable, MenuItemID: "menu-1"},
		{Code: OrderTotalBelowMinimum},
	}, result.Reasons)
}

func TestOrderItemsOrderableAcceptsInclusiveAmountBounds(t *testing.T) {
	order := newTestOrder(t)
	availability := ItemAvailability{"menu-1": true}

	result := OrderItemsOrderable(order, OrderabilityContext{
		Availability: availability,
		MinimumTotal: mustPolicyMoney(t, "10"),
		MaximumTotal: mustPolicyMoney(t, "10"),
	})

	assert.True(t, result.Satisfied())
	assert.Empty(t, result.Reasons)
}

func TestOrderItemsOrderableRejectsTotalAboveMaximum(t *testing.T) {
	order := newTestOrder(t)

	result := OrderItemsOrderable(order, OrderabilityContext{
		Availability: ItemAvailability{"menu-1": true},
		MinimumTotal: mustPolicyMoney(t, "1"),
		MaximumTotal: mustPolicyMoney(t, "9.99"),
	})

	assert.Equal(t, []PolicyReason{{Code: OrderTotalAboveMaximum}}, result.Reasons)
}

func mustPolicyMoney(t *testing.T, amount string) vo.Money {
	t.Helper()
	money, err := vo.ParseMoney(amount, "EUR")
	require.NoError(t, err)
	return money
}
