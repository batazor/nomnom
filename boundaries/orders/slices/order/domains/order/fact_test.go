package order

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Facts that only add to the journal: the Order keeps its State.
func TestJournalFactsKeepStateAndRecordOnce(t *testing.T) {
	recorders := map[FulfillmentFact]func(*Order, time.Time) (TransitionResult, error){
		FactRestaurantRejected: func(o *Order, at time.Time) (TransitionResult, error) {
			return o.RecordRestaurantRejection(at, "kitchen_closed")
		},
		FactCourierAssigned:   (*Order).RecordCourierAssignment,
		FactDeliveryCompleted: (*Order).RecordDeliveryCompletion,
		FactDeliveryFailed: func(o *Order, at time.Time) (TransitionResult, error) {
			return o.RecordDeliveryFailure(at, "customer_unreachable")
		},
	}

	for fact, record := range recorders {
		for _, state := range allStates {
			t.Run(string(fact)+"/"+string(state), func(t *testing.T) {
				order := orderInState(t, state)

				result, err := record(order, testNow)
				if state.IsFinal() {
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

// Facts of neighbours arrive from separate topics: a fact moves the Order only
// forward, and a late one is only added to the journal.
func TestProgressFactsAreMonotonic(t *testing.T) {
	tests := []struct {
		name   string
		from   State
		record func(*Order, time.Time) (TransitionResult, error)
		fact   FulfillmentFact
		want   State
	}{
		{"preparation after confirm", StateConfirmed, (*Order).RecordPreparationStarted, FactPreparationStarted, StatePreparing},
		{"ready after preparation", StatePreparing, (*Order).RecordReadyForPickup, FactReadyForPickup, StateReady},
		{"pickup after ready", StateReady, (*Order).RecordPickup, FactPickedUp, StatePickedUp},
		{"preparation jumps over confirm", StatePaid, (*Order).RecordPreparationStarted, FactPreparationStarted, StatePreparing},
		{"pickup jumps over ready", StateConfirmed, (*Order).RecordPickup, FactPickedUp, StatePickedUp},
		{"late ready stays", StatePickedUp, (*Order).RecordReadyForPickup, FactReadyForPickup, StatePickedUp},
		{"late preparation stays", StateReady, (*Order).RecordPreparationStarted, FactPreparationStarted, StateReady},
		{"late confirm stays", StatePreparing, (*Order).Confirm, FactRestaurantAccepted, StatePreparing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := orderInState(t, tt.from)

			result, err := tt.record(order, testNow)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.To)
			assert.Equal(t, tt.want, order.State())
			assert.True(t, order.HasFact(tt.fact))
			assert.Equal(t, testNow, order.Facts()[0].OccurredAt)

			_, err = tt.record(order, testNow.Add(time.Minute))
			assert.Equal(t, FactAlreadyRecorded{Fact: tt.fact}, err)
			assert.Len(t, order.Facts(), 1)
			assert.Equal(t, tt.want, order.State())
		})
	}
}

// The picked up Order keeps its State whatever order the kitchen facts come in.
func TestOutOfOrderFactsConverge(t *testing.T) {
	order := orderInState(t, StatePaid)

	for _, record := range []func(*Order, time.Time) (TransitionResult, error){
		(*Order).RecordPickup,
		(*Order).RecordReadyForPickup,
		(*Order).RecordPreparationStarted,
		(*Order).Confirm,
	} {
		_, err := record(order, testNow)
		require.NoError(t, err)
	}

	assert.Equal(t, StatePickedUp, order.State())
	assert.Len(t, order.Facts(), 4)
}

// FR-639: the payment moves a placed Order to paid, once, and only before the
// restaurant accepted.
func TestRecordPaymentAuthorization(t *testing.T) {
	for _, state := range allStates {
		t.Run(string(state), func(t *testing.T) {
			order := orderInState(t, state)

			result, err := order.RecordPaymentAuthorization(testNow)
			if state != StatePlaced && state != StatePaid {
				assert.Equal(t, InvalidTransition{From: state, Trigger: TriggerAuthorizePayment}, err)
				assert.Empty(t, order.Facts())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, TransitionResult{From: state, Trigger: TriggerAuthorizePayment, To: StatePaid}, result)
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
