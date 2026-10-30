package order

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A courier assignment belongs to Delivery: the Order keeps its State and history.
func TestAdmitCourierAssignmentRefusesOnlyFinalOrder(t *testing.T) {
	for _, state := range allStates {
		t.Run(string(state), func(t *testing.T) {
			order := orderInState(t, state)

			err := order.AdmitCourierAssignment(testNow)
			if state.IsFinal() {
				assert.Equal(t, InvalidTransition{From: state, Trigger: TriggerUpdateDetails}, err)
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, order.Transitions())
			assert.Equal(t, state, order.State())
			assert.Empty(t, order.PullEvents())
		})
	}
}

// A known outcome moves any Order in progress and ends its progress.
func TestOutcomeStepsEndProgress(t *testing.T) {
	outcomes := []struct {
		record  func(*Order, time.Time) (TransitionResult, error)
		trigger Trigger
		want    State
	}{
		{(*Order).RecordRestaurantRejection, TriggerReject, StateRejected},
		{(*Order).RecordDeliveryFailure, TriggerFailDelivery, StateDeliveryFailed},
		{(*Order).RecordDeliveryCompletion, TriggerDeliver, StateDelivered},
	}
	for _, outcome := range outcomes {
		for _, from := range []State{StatePlaced, StatePaid, StateConfirmed, StatePreparing, StateReady, StatePickedUp} {
			t.Run(string(outcome.trigger)+"/"+string(from), func(t *testing.T) {
				order := orderInState(t, from)

				result, err := outcome.record(order, testNow)
				require.NoError(t, err)
				assert.Equal(t, outcome.want, result.To)
				assert.True(t, order.HasApplied(outcome.trigger))

				_, err = outcome.record(order, testNow.Add(time.Minute))
				assert.Equal(t, TransitionAlreadyApplied{Trigger: outcome.trigger}, err)

				_, err = order.RecordPickup(testNow)
				require.NoError(t, err)
				assert.Equal(t, outcome.want, order.State(), "a late step only joins the history")
				assert.True(t, order.HasApplied(TriggerPickUp))
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

// Events of neighbours arrive from separate topics: a step moves the Order only
// forward, and a late one is only added to the history.
func TestProgressStepsAreMonotonic(t *testing.T) {
	tests := []struct {
		name    string
		from    State
		record  func(*Order, time.Time) (TransitionResult, error)
		trigger Trigger
		want    State
	}{
		{"preparation after confirm", StateConfirmed, (*Order).RecordPreparationStarted, TriggerStartPreparation, StatePreparing},
		{"ready after preparation", StatePreparing, (*Order).RecordReadyForPickup, TriggerMarkReady, StateReady},
		{"pickup after ready", StateReady, (*Order).RecordPickup, TriggerPickUp, StatePickedUp},
		{"preparation jumps over confirm", StatePaid, (*Order).RecordPreparationStarted, TriggerStartPreparation, StatePreparing},
		{"pickup jumps over ready", StateConfirmed, (*Order).RecordPickup, TriggerPickUp, StatePickedUp},
		{"late ready stays", StatePickedUp, (*Order).RecordReadyForPickup, TriggerMarkReady, StatePickedUp},
		{"late preparation stays", StateReady, (*Order).RecordPreparationStarted, TriggerStartPreparation, StateReady},
		{"late confirm stays", StatePreparing, (*Order).Confirm, TriggerConfirm, StatePreparing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := orderInState(t, tt.from)

			result, err := tt.record(order, testNow)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.To)
			assert.Equal(t, tt.want, order.State())
			assert.True(t, order.HasApplied(tt.trigger))
			assert.Equal(t, testNow, order.Transitions()[0].OccurredAt)

			_, err = tt.record(order, testNow.Add(time.Minute))
			assert.Equal(t, TransitionAlreadyApplied{Trigger: tt.trigger}, err)
			assert.Len(t, order.Transitions(), 1)
			assert.Equal(t, tt.want, order.State())
		})
	}
}

// The picked up Order keeps its State whatever order the kitchen steps come in.
func TestOutOfOrderStepsConverge(t *testing.T) {
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
	assert.Len(t, order.Transitions(), 4)
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
				assert.Empty(t, order.Transitions())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, TransitionResult{From: state, Trigger: TriggerAuthorizePayment, To: StatePaid}, result)
			assert.True(t, order.HasApplied(TriggerAuthorizePayment))

			_, err = order.RecordPaymentAuthorization(testNow.Add(time.Minute))
			assert.Equal(t, TransitionAlreadyApplied{Trigger: TriggerAuthorizePayment}, err)
			assert.Len(t, order.Transitions(), 1)
			assert.Empty(t, order.PullEvents())
		})
	}
}

func TestStepRequiresOccurredAt(t *testing.T) {
	order := newTestOrder(t)

	_, err := order.RecordPickup(time.Time{})
	assert.Equal(t, GuardFailed{Reason: "occurred_at_required"}, err)
	assert.Empty(t, order.Transitions())
}

// The whole lifecycle joins the history: the Saga ends an Order too.
func TestHistoryKeepsEveryAppliedTrigger(t *testing.T) {
	order := newTestOrder(t)
	reason, err := NewCancellationReason(OutcomeFailure, "restaurant_rejected")
	require.NoError(t, err)

	_, err = order.RecordRestaurantRejection(testNow)
	require.NoError(t, err)
	_, err = order.Cancel(testNow.Add(time.Minute), reason)
	require.NoError(t, err)

	assert.Equal(t, []AppliedTransition{
		{Trigger: TriggerReject, OccurredAt: testNow},
		{Trigger: TriggerCancel, OccurredAt: testNow.Add(time.Minute)},
	}, order.Transitions())
}

func TestCompleteJoinsHistory(t *testing.T) {
	order := orderInState(t, StateDelivered)

	_, err := order.Complete(testNow)
	require.NoError(t, err)

	assert.Equal(t, []AppliedTransition{{Trigger: TriggerComplete, OccurredAt: testNow}}, order.Transitions())
}
