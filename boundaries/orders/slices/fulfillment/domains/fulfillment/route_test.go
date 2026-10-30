package fulfillment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// AC-433: the route decides every step once in order and ends in Completion.
func TestProcessRunsRouteToCompletion(t *testing.T) {
	s := newProcess(t)
	s.run()
	s.signal(fulfillment.SignalRestaurantOrderAccepted)
	s.run()
	s.signal(fulfillment.SignalPreparationStarted)
	s.signal(fulfillment.SignalCourierAssignedToDelivery)
	s.signal(fulfillment.SignalRestaurantOrderReady)
	s.signal(fulfillment.SignalDeliveryPickedUp)
	s.run()
	require.False(t, s.p.Completed())
	s.signal(fulfillment.SignalDeliveryCompleted)
	s.run()

	assert.Equal(t, []string{
		"authorize_payment", "record_payment_authorization", "request_restaurant_order", "create_delivery", "capture_payment", "complete_order",
	}, keysOf(s.steps))
	assert.Equal(t, fulfillment.StageCompleting, s.steps[5].Stage, "completion runs in its own stage")
	assert.True(t, s.p.Completed())
	assert.Equal(t, fulfillment.StageCompleted, s.p.Stage())
	outcome, code := s.outcome()
	assert.Equal(t, fulfillment.OutcomeCompleted, outcome)
	assert.Empty(t, code)
	_, ok := s.p.Next()
	assert.False(t, ok, "a completed process decides nothing")
}

// The route waits for the signal that unlocks the next step.
func TestProcessNextWaitsForSignals(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*process)
		want    fulfillment.StepKind
	}{
		"fresh process authorizes":       {arrange: func(*process) {}, want: fulfillment.StepAuthorizePayment},
		"waits for acceptance":           {arrange: func(s *process) { s.run() }},
		"acceptance creates delivery":    {arrange: func(s *process) { s.run(); s.signal(fulfillment.SignalRestaurantOrderAccepted) }, want: fulfillment.StepCreateDelivery},
		"waits for assignment and ready": {arrange: func(s *process) { s.accepted() }},
		"waits for pickup": {arrange: func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalCourierAssignedToDelivery)
			s.signal(fulfillment.SignalRestaurantOrderReady)
		}},
		"pickup captures": {arrange: func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalCourierAssignedToDelivery)
			s.signal(fulfillment.SignalRestaurantOrderReady)
			s.signal(fulfillment.SignalDeliveryPickedUp)
		}, want: fulfillment.StepCapturePayment},
		"waits for delivery": {arrange: func(s *process) { s.pickedUp() }},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			tc.arrange(s)

			step, ok := s.p.Next()

			assert.Equal(t, tc.want != "", ok)
			assert.Equal(t, tc.want, step.Kind)
		})
	}
}

// AC-434, the declined Capture: a payment decline is a failure, not a step.
func TestProcessFailsOnDeclinedPayment(t *testing.T) {
	for name, tc := range map[string]struct {
		declined fulfillment.StepKind
		arrange  func(*process)
		reason   string
		saga     []fulfillment.StepKind
	}{
		"authorization": {
			declined: fulfillment.StepAuthorizePayment,
			arrange:  func(s *process) { s.run() },
			reason:   fulfillment.ReasonPaymentDeclined,
			saga:     []fulfillment.StepKind{fulfillment.StepFailOrCancelOrder},
		},
		"capture": {
			declined: fulfillment.StepCapturePayment,
			arrange:  func(s *process) { s.pickedUp() },
			reason:   fulfillment.ReasonPaymentCaptureDeclined,
			saga: []fulfillment.StepKind{
				fulfillment.StepCancelDelivery, fulfillment.StepCancelPaymentAuthorization, fulfillment.StepFailOrCancelOrder,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			s.declined[tc.declined] = true
			tc.arrange(s)

			require.True(t, s.p.Failed())
			outcome, code := s.outcome()
			assert.Equal(t, fulfillment.OutcomeFailure, outcome)
			assert.Equal(t, tc.reason, code)
			assert.Equal(t, tc.saga, kindsOf(s.p.Compensation()))
			_, ok := s.p.Next()
			assert.False(t, ok, "a failed process runs only the Saga")
		})
	}
}

func TestProcessStage(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*process)
		want    fulfillment.Stage
	}{
		"authorizing":        {arrange: func(*process) {}, want: fulfillment.StageAuthorizingPayment},
		"waiting acceptance": {arrange: func(s *process) { s.run() }, want: fulfillment.StageWaitingAcceptance},
		"preparing":          {arrange: func(s *process) { s.accepted() }, want: fulfillment.StagePreparing},
		"assigned not ready": {arrange: func(s *process) { s.accepted(); s.signal(fulfillment.SignalCourierAssignedToDelivery) }, want: fulfillment.StagePreparing},
		"waiting pickup": {arrange: func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalCourierAssignedToDelivery)
			s.signal(fulfillment.SignalRestaurantOrderReady)
		}, want: fulfillment.StageWaitingPickup},
		"delivering": {arrange: func(s *process) { s.pickedUp() }, want: fulfillment.StageDelivering},
		"compensating": {arrange: func(s *process) {
			s.run()
			s.signal(fulfillment.SignalRestaurantOrderRejected)
			s.p.Compensate()
		}, want: fulfillment.StageCompensating},
		"cancelled": {arrange: func(s *process) {
			s.run()
			s.signal(fulfillment.SignalRestaurantOrderRejected)
			for _, step := range s.p.Compensate() {
				s.p.Confirm(step, true, s.now)
			}
		}, want: fulfillment.StageCancelled},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			tc.arrange(s)

			assert.Equal(t, tc.want, s.p.Stage())
		})
	}
}

// The kitchen learns about the Pickup from the route, only once it reported the
// order ready; a run without the patch has no such step.
func TestProcessReportsPickupToRestaurant(t *testing.T) {
	s := newProcess(t)
	s.p.ReportPickupToRestaurant()
	s.run()
	s.signal(fulfillment.SignalRestaurantOrderAccepted)
	s.run()
	s.signal(fulfillment.SignalCourierAssignedToDelivery)
	s.signal(fulfillment.SignalDeliveryPickedUp)
	step, ok := s.p.Next()
	require.True(t, ok)
	assert.Equal(t, fulfillment.StepCapturePayment, step.Kind, "the money does not wait for the kitchen")
	s.run()

	s.signal(fulfillment.SignalRestaurantOrderReady)
	step, ok = s.p.Next()
	require.True(t, ok)
	assert.Equal(t, fulfillment.StepRecordRestaurantPickup, step.Kind)
	s.run()
	assert.False(t, s.p.PickedUpAt().IsZero())
	_, ok = s.p.Next()
	assert.False(t, ok, "the step runs once")
}
