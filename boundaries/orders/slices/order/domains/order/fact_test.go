package order

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	// restaurant_accepted is recorded by Confirm with its transition, not by
	// TriggerRecordFact (TestConfirmRecordsRestaurantAcceptance);
	// payment_authorized is admitted only in placed
	// (TestRecordPaymentAuthorizationOnlyInPlaced).
	require.Len(t, recorders, len(allFacts)-2)
	require.NotContains(t, recorders, FactRestaurantAccepted)
	require.NotContains(t, recorders, FactPaymentAuthorized)

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

// FR-639: the payment is recorded once, and only before the restaurant accepted.
func TestRecordPaymentAuthorizationOnlyInPlaced(t *testing.T) {
	for _, state := range allStates {
		t.Run(string(state), func(t *testing.T) {
			order := orderInState(t, state)

			result, err := order.RecordPaymentAuthorization(testNow)
			if state != StatePlaced {
				assert.Equal(t, InvalidTransition{From: state, Trigger: TriggerRecordFact}, err)
				assert.Empty(t, order.Facts())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, TransitionResult{From: state, Trigger: TriggerRecordFact, To: state}, result)
			assert.True(t, order.HasFact(FactPaymentAuthorized))

			_, err = order.RecordPaymentAuthorization(testNow.Add(time.Minute))
			assert.Equal(t, FactAlreadyRecorded{Fact: FactPaymentAuthorized}, err)
			assert.Len(t, order.Facts(), 1)
			assert.Empty(t, order.PullEvents())
		})
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
