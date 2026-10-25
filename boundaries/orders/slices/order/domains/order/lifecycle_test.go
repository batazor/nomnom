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
			}}, order.PullEvents())
		})
	}
}

func TestCompleteRecordsOrderCompletedOnce(t *testing.T) {
	order := orderInState(t, StateConfirmed)

	_, err := order.Complete()
	require.NoError(t, err)
	assert.Equal(t, []DomainEvent{OrderCompleted{OrderID: "order-1"}}, order.PullEvents())

	_, err = order.Complete()
	var already AlreadyInState
	require.True(t, errors.As(err, &already))
	assert.Empty(t, order.PullEvents())
}

func TestCancellationReasonValidation(t *testing.T) {
	tests := []struct {
		name    string
		outcome NegativeOutcome
		code    string
		valid   bool
	}{
		{name: "cancellation", outcome: OutcomeCancellation, code: "customer_requested", valid: true},
		{name: "failure", outcome: OutcomeFailure, code: "restaurant_rejected", valid: true},
		{name: "unknown outcome", outcome: "refund", code: "x"},
		{name: "empty code", outcome: OutcomeFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, err := NewCancellationReason(tt.outcome, tt.code)
			if !tt.valid {
				require.ErrorIs(t, err, ErrInvalidCancellationReason)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.outcome, reason.Outcome())
			assert.Equal(t, tt.code, reason.Code())
		})
	}

	_, err := newTestOrder(t).Cancel(testNow, CancellationReason{})
	require.ErrorIs(t, err, ErrInvalidCancellationReason)
}

func TestRecordFactFollowsStateAndRecordsOnce(t *testing.T) {
	recorders := map[FulfillmentFact]func(*Order, time.Time) (TransitionResult, error){
		FactRestaurantRejected: func(o *Order, at time.Time) (TransitionResult, error) {
			return o.RecordRestaurantRejection(at, "kitchen_closed")
		},
		FactPreparationStarted: (*Order).RecordPreparationStarted,
		FactReadyForPickup:     (*Order).RecordReadyForPickup,
		FactCourierAssigned:    (*Order).RecordCourierAssignment,
		FactPickedUp:           (*Order).RecordPickup,
		FactDeliveryCompleted:  (*Order).RecordDeliveryCompletion,
		FactDeliveryFailed: func(o *Order, at time.Time) (TransitionResult, error) {
			return o.RecordDeliveryFailure(at, "customer_unreachable")
		},
	}
	require.Len(t, recorders, len(allFacts))

	for fact, record := range recorders {
		for _, state := range allStates {
			t.Run(string(fact)+"/"+string(state), func(t *testing.T) {
				order := orderInState(t, state)

				result, err := record(order, testNow)
				if state == StateCancelled || state == StateCompleted {
					assert.Equal(t, InvalidTransition{From: state, Trigger: TriggerRecordFact}, err)
					assert.Empty(t, order.Facts())
					return
				}
				require.NoError(t, err)
				assert.Equal(t, TransitionResult{From: state, Trigger: TriggerRecordFact, To: state}, result)
				require.Len(t, order.Facts(), 1)
				assert.Equal(t, fact, order.Facts()[0].Fact)
				assert.Equal(t, testNow, order.Facts()[0].OccurredAt)
				assert.True(t, order.HasFact(fact))

				_, err = record(order, testNow.Add(time.Minute))
				assert.Equal(t, FactAlreadyRecorded{Fact: fact}, err)
				assert.Len(t, order.Facts(), 1)
				assert.Equal(t, state, order.State())
				assert.Empty(t, order.PullEvents())
			})
		}
	}
}

func TestRecordFactRequiresOccurredAt(t *testing.T) {
	order := newTestOrder(t)

	_, err := order.RecordPickup(time.Time{})
	assert.Equal(t, GuardFailed{Reason: "occurred_at_required"}, err)
	assert.Empty(t, order.Facts())
}

func TestRecordedReasonIsKept(t *testing.T) {
	order := newTestOrder(t)

	_, err := order.RecordRestaurantRejection(testNow, "kitchen_closed")
	require.NoError(t, err)
	assert.Equal(t, []RecordedFact{{Fact: FactRestaurantRejected, OccurredAt: testNow, Reason: "kitchen_closed"}}, order.Facts())
}

func TestCheckCancellationUsesWindowThenStateWithoutChangingOrder(t *testing.T) {
	window := CancellationWindow{OpensAt: testNow, ClosesAt: testNow.Add(10 * time.Minute)}

	tests := []struct {
		name        string
		state       State
		requestedAt time.Time
		wantErr     error
	}{
		{name: "placed inside window", state: StatePlaced, requestedAt: testNow.Add(time.Minute)},
		{name: "confirmed at deadline", state: StateConfirmed, requestedAt: window.ClosesAt},
		{
			name:        "before window",
			state:       StatePlaced,
			requestedAt: testNow.Add(-time.Second),
			wantErr:     CancellationNotAllowed{Reasons: []PolicyReason{{Code: CancellationWindowNotOpen}}},
		},
		{
			name:        "after window",
			state:       StateConfirmed,
			requestedAt: window.ClosesAt.Add(time.Nanosecond),
			wantErr:     CancellationNotAllowed{Reasons: []PolicyReason{{Code: CancellationWindowExpired}}},
		},
		{name: "already cancelled", state: StateCancelled, requestedAt: testNow, wantErr: AlreadyInState{State: StateCancelled}},
		{
			name:        "completed",
			state:       StateCompleted,
			requestedAt: testNow,
			wantErr:     InvalidTransition{From: StateCompleted, Trigger: TriggerCancel},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := orderInState(t, tt.state)

			err := order.CheckCancellation(tt.requestedAt, window)
			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				assert.Equal(t, tt.wantErr, err)
			}
			assert.Equal(t, tt.state, order.State())
			assert.Empty(t, order.PullEvents())
		})
	}
}

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

	foreign := snapshot
	foreign.Facts = []RecordedFact{{Fact: "payment_captured", OccurredAt: testNow}}
	_, err = Restore(foreign)
	require.ErrorIs(t, err, ErrInvalidSnapshot)
}
