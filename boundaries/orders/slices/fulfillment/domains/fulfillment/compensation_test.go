package fulfillment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// The Saga compensates confirmed steps in reverse order, skips steps closed by
// their owner and ends by recording the negative outcome.
func TestProcessPlansCompensation(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*process)
		outcome fulfillment.Outcome
		reason  string
		saga    []fulfillment.StepKind
	}{
		// AC-547: no Delivery was created, so none is cancelled.
		"rejected before acceptance": {
			arrange: func(s *process) {
				s.run()
				s.signal(fulfillment.SignalRestaurantOrderRejected)
			},
			outcome: fulfillment.OutcomeFailure, reason: fulfillment.ReasonRestaurantRejected,
			saga: []fulfillment.StepKind{fulfillment.StepCancelPaymentAuthorization, fulfillment.StepFailOrCancelOrder},
		},
		// AC-435
		"acceptance timeout": {
			arrange: func(s *process) {
				s.run()
				s.expire()
			},
			outcome: fulfillment.OutcomeFailure, reason: fulfillment.ReasonRestaurantAcceptanceTimeout,
			saga: []fulfillment.StepKind{
				fulfillment.StepCancelRestaurantOrder, fulfillment.StepCancelPaymentAuthorization, fulfillment.StepFailOrCancelOrder,
			},
		},
		// AC-438: the rejected Restaurant Order is closed by its owner.
		"rejected after acceptance": {
			arrange: func(s *process) {
				s.accepted()
				s.signal(fulfillment.SignalRestaurantOrderRejected)
			},
			outcome: fulfillment.OutcomeFailure, reason: fulfillment.ReasonRestaurantRejected,
			saga: []fulfillment.StepKind{
				fulfillment.StepCancelDelivery, fulfillment.StepCancelPaymentAuthorization, fulfillment.StepFailOrCancelOrder,
			},
		},
		"preparation failed": {
			arrange: func(s *process) {
				s.accepted()
				s.signal(fulfillment.SignalPreparationFailed)
			},
			outcome: fulfillment.OutcomeFailure, reason: fulfillment.ReasonPreparationFailed,
			saga: []fulfillment.StepKind{
				fulfillment.StepCancelDelivery, fulfillment.StepCancelPaymentAuthorization, fulfillment.StepFailOrCancelOrder,
			},
		},
		// AC-439: after Capture the payment is refunded; the Delivery is closed by
		// its owner and the Restaurant Order is not cancelled after Pickup.
		"delivery failed after capture": {
			arrange: func(s *process) {
				s.pickedUp()
				s.signal(fulfillment.SignalDeliveryFailed)
			},
			outcome: fulfillment.OutcomeFailure, reason: fulfillment.ReasonDeliveryFailed,
			saga: []fulfillment.StepKind{fulfillment.StepRefundPayment, fulfillment.StepFailOrCancelOrder},
		},
		"delivery timeout": {
			arrange: func(s *process) {
				s.pickedUp()
				s.expire()
			},
			outcome: fulfillment.OutcomeFailure, reason: fulfillment.ReasonDeliveryTimeout,
			saga: []fulfillment.StepKind{fulfillment.StepCancelDelivery, fulfillment.StepRefundPayment, fulfillment.StepFailOrCancelOrder},
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			tc.arrange(s)
			require.True(t, s.p.Failed())

			plan := s.p.Compensate()

			assert.Equal(t, tc.saga, kindsOf(plan))
			outcome, code := s.outcome()
			assert.Equal(t, tc.outcome, outcome)
			assert.Equal(t, tc.reason, code)
			assert.Equal(t, fulfillment.StageCompensating, s.p.Stage())
			assert.False(t, s.p.Failed(), "the Saga starts once")
			for _, step := range plan {
				s.p.Confirm(step, true, s.now)
			}
			assert.Equal(t, fulfillment.StageCancelled, s.p.Stage())
		})
	}
}

// Compensation steps keep stable idempotency keys.
func TestProcessCompensationKeys(t *testing.T) {
	s := newProcess(t).accepted()
	require.NoError(t, s.p.RequestCancellation(fulfillment.CancellationRequest{RequestID: "cancel-1"}))

	assert.Equal(t, []string{"cancel_delivery", "cancel_restaurant_order", "cancel_payment_authorization", ""}, keysOf(s.p.Compensation()))
}
