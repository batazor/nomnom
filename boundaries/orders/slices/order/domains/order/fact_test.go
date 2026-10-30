package order

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A courier assignment belongs to Delivery: the Order keeps its State.
func TestCourierAssignmentKeepsStateAndRecordsOnce(t *testing.T) {
	for _, state := range allStates {
		t.Run(string(state), func(t *testing.T) {
			order := orderInState(t, state)

			result, err := order.RecordCourierAssignment(testNow)
			if state.IsFinal() {
				assert.Equal(t, InvalidTransition{From: state, Trigger: TriggerRecordFact}, err)
				assert.Empty(t, order.Facts())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, TransitionResult{From: state, Trigger: TriggerRecordFact, To: state}, result)
			assert.True(t, order.HasFact(FactCourierAssigned))

			_, err = order.RecordCourierAssignment(testNow.Add(time.Minute))
			assert.Equal(t, FactAlreadyRecorded{Fact: FactCourierAssigned}, err)
			assert.Len(t, order.Facts(), 1)
			assert.Equal(t, state, order.State())
			assert.Empty(t, order.PullEvents())
		})
	}
}

// A known outcome moves any Order in progress and ends its progress.
func TestOutcomeFactsEndProgress(t *testing.T) {
	outcomes := []struct {
		record func(*Order, time.Time) (TransitionResult, error)
		fact   FulfillmentFact
		want   State
	}{
		{func(o *Order, at time.Time) (TransitionResult, error) {
			return o.RecordRestaurantRejection(at, "kitchen_closed")
		}, FactRestaurantRejected, StateRejected},
		{func(o *Order, at time.Time) (TransitionResult, error) {
			return o.RecordDeliveryFailure(at, "customer_unreachable")
		}, FactDeliveryFailed, StateDeliveryFailed},
		{(*Order).RecordDeliveryCompletion, FactDeliveryCompleted, StateDelivered},
	}
	for _, outcome := range outcomes {
		for _, from := range []State{StatePlaced, StatePaid, StateConfirmed, StatePreparing, StateReady, StatePickedUp} {
			t.Run(string(outcome.fact)+"/"+string(from), func(t *testing.T) {
				order := orderInState(t, from)

				result, err := outcome.record(order, testNow)
				require.NoError(t, err)
				assert.Equal(t, outcome.want, result.To)
				assert.True(t, order.HasFact(outcome.fact))

				_, err = outcome.record(order, testNow.Add(time.Minute))
				assert.Equal(t, FactAlreadyRecorded{Fact: outcome.fact}, err)

				_, err = order.RecordPickup(testNow)
				require.NoError(t, err)
				assert.Equal(t, outcome.want, order.State(), "a late fact only joins the journal")
				assert.True(t, order.HasFact(FactPickedUp))
			})
		}
	}
}

// The customer cannot cancel an Order with a known outcome; the Saga can.
func TestCancellationOfSettlingOrder(t *testing.T) {
	window := CancellationWindow{OpensAt: testNow.Add(-time.Hour), ClosesAt: testNow.Add(time.Hour)}
	reason, err := NewCancellationReason(OutcomeFailure, "delivery_failed")
	require.NoError(t, err)
	for _, state := range []State{StateRejected, StateDeliveryFailed, StateDelivered} {
		t.Run(string(state), func(t *testing.T) {
			order := orderInState(t, state)

			assert.Equal(t, InvalidTransition{From: state, Trigger: TriggerCancel}, order.CheckCancellation(testNow, window))

			result, err := order.Cancel(testNow, reason)
			require.NoError(t, err)
			assert.Equal(t, StateCancelled, result.To)
		})
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
