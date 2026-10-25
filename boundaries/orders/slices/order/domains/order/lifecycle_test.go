package order

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/order/domains/order/vo"
)

var testNow = time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)

func orderInState(t *testing.T, state State) *Order {
	t.Helper()
	snapshot := newTestOrder(t).Snapshot()
	snapshot.State = state
	snapshot.PlacedAt = testNow
	order, err := Restore(snapshot)
	require.NoError(t, err)
	return order
}

func orderability(t *testing.T, available bool, minimum, maximum string) OrderabilityContext {
	t.Helper()
	return OrderabilityContext{
		Availability: ItemAvailability{"menu-1": available},
		MinimumTotal: mustPolicyMoney(t, minimum),
		MaximumTotal: mustPolicyMoney(t, maximum),
	}
}

func TestPlaceChecksOrderabilityAndRecordsOrderPlaced(t *testing.T) {
	price := mustPolicyMoney(t, "10")
	quantity, err := vo.NewQuantity(2)
	require.NoError(t, err)
	address, err := vo.NewDeliveryAddress("Main street 1")
	require.NoError(t, err)
	items := []Item{NewItem("menu-1", quantity, price)}

	tests := []struct {
		name     string
		placedAt time.Time
		context  OrderabilityContext
		wantErr  error
		reasons  []PolicyReasonCode
	}{
		{name: "orderable", placedAt: testNow, context: orderability(t, true, "0", "100")},
		{name: "placed_at required", context: orderability(t, true, "0", "100"), wantErr: ErrPlacedAtRequired},
		{
			name:     "unavailable and below minimum",
			placedAt: testNow,
			context:  orderability(t, false, "50", "100"),
			reasons:  []PolicyReasonCode{OrderItemUnavailable, OrderTotalBelowMinimum},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, err := Place("order-1", "customer-1", "restaurant-1", items, address, tt.placedAt, tt.context)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			if tt.reasons != nil {
				var notOrderable NotOrderable
				require.True(t, errors.As(err, &notOrderable))
				codes := make([]PolicyReasonCode, 0, len(notOrderable.Reasons))
				for _, reason := range notOrderable.Reasons {
					codes = append(codes, reason.Code)
				}
				assert.Equal(t, tt.reasons, codes)
				assert.Nil(t, order)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, StatePlaced, order.State())
			assert.Equal(t, testNow, order.PlacedAt())
			assert.Equal(t, []DomainEvent{OrderPlaced{
				OrderID:      "order-1",
				CustomerID:   "customer-1",
				RestaurantID: "restaurant-1",
				Total:        mustPolicyMoney(t, "20"),
				PlacedAt:     testNow,
				Items:        items,
			}}, order.PullEvents())
		})
	}
}

func TestCompleteRecordsOrderCompletedOnce(t *testing.T) {
	order := orderInState(t, StateConfirmed)

	completedAt := testNow.Add(time.Hour)
	_, err := order.Complete(completedAt)
	require.NoError(t, err)
	assert.Equal(t, []DomainEvent{OrderCompleted{OrderID: "order-1", PlacedAt: testNow, CompletedAt: completedAt}}, order.PullEvents())

	_, err = order.Complete(completedAt.Add(time.Minute))
	var already AlreadyInState
	require.True(t, errors.As(err, &already))
	assert.Empty(t, order.PullEvents())
}

func TestCompleteRequiresCompletedAt(t *testing.T) {
	order := orderInState(t, StateConfirmed)

	_, err := order.Complete(time.Time{})
	var guard GuardFailed
	require.True(t, errors.As(err, &guard))
	assert.Equal(t, StateConfirmed, order.State())
	assert.Empty(t, order.PullEvents())
}
