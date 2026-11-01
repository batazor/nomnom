package fulfillment_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// Signals the current state does not admit are refused with a diagnostic reason
// and change nothing.
func TestProcessRefusesSignalsOutOfOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*process)
		signal  fulfillment.SignalKind
		reason  string
	}{
		"accepted twice":            {arrange: func(s *process) { s.accepted() }, signal: fulfillment.SignalRestaurantOrderAccepted, reason: "already accepted"},
		"started before acceptance": {arrange: func(s *process) { s.run() }, signal: fulfillment.SignalPreparationStarted, reason: "not accepted"},
		"ready twice": {arrange: func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalRestaurantOrderReady)
		}, signal: fulfillment.SignalRestaurantOrderReady, reason: "already ready"},
		"preparation failed after ready": {arrange: func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalRestaurantOrderReady)
		}, signal: fulfillment.SignalPreparationFailed, reason: "already ready"},
		"assigned twice": {arrange: func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalCourierAssignedToDelivery)
		}, signal: fulfillment.SignalCourierAssignedToDelivery, reason: "already assigned"},
		"picked up twice":       {arrange: func(s *process) { s.pickedUp() }, signal: fulfillment.SignalDeliveryPickedUp, reason: "already picked up"},
		"rejected after pickup": {arrange: func(s *process) { s.pickedUp() }, signal: fulfillment.SignalRestaurantOrderRejected, reason: "rejected after pickup"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			tc.arrange(s)
			stage, deadline := s.p.Stage(), s.p.NextDeadline()

			effect := s.signal(tc.signal)

			assert.Equal(t, fulfillment.Effect{Verdict: fulfillment.Refused, Reason: tc.reason}, effect)
			assert.False(t, effect.Changed())
			assert.Equal(t, stage, s.p.Stage())
			assert.Equal(t, deadline, s.p.NextDeadline())
			assert.False(t, s.p.Failed())
		})
	}
}

// AC-441: a signal ahead of its step changes nothing until the step is confirmed
// and is then applied at the time it was received.
func TestProcessAppliesEarlySignalsAfterTheirStep(t *testing.T) {
	t.Run("acceptance before the request is confirmed", func(t *testing.T) {
		s := newProcess(t)

		assert.Equal(t, fulfillment.Remembered, s.signal(fulfillment.SignalRestaurantOrderAccepted).Verdict)
		s.at(time.Minute).run()

		assert.Equal(t, []fulfillment.StepKind{
			fulfillment.StepAuthorizePayment,
			fulfillment.StepRecordPaymentAuthorization,
			fulfillment.StepRequestRestaurantOrder,
			fulfillment.StepCreateDelivery,
		}, kindsOf(s.steps))
		assert.Equal(t, start.Add(fulfillment.DefaultPolicy().PreparationTimeout), s.p.NextDeadline())
	})

	t.Run("readiness before acceptance", func(t *testing.T) {
		s := newProcess(t)
		s.run()
		deadline := s.p.NextDeadline()

		assert.Equal(t, fulfillment.Remembered, s.signal(fulfillment.SignalRestaurantOrderReady).Verdict)
		assert.Equal(t, deadline, s.p.NextDeadline())

		s.at(time.Minute).signal(fulfillment.SignalRestaurantOrderAccepted)
		s.run()
		assert.True(t, s.p.NextDeadline().IsZero(), "the kitchen is ready, the courier has no deadline")
	})

	t.Run("delay before acceptance extends preparation", func(t *testing.T) {
		s := newProcess(t)
		s.run()
		expected := start.Add(fulfillment.DefaultPolicy().PreparationTimeout + time.Minute)

		effect := s.p.Apply(fulfillment.Signal{Kind: fulfillment.SignalPreparationDelayed, EventID: "delayed", ExpectedReadyAt: expected}, s.now)
		assert.Equal(t, fulfillment.Remembered, effect.Verdict)
		s.signal(fulfillment.SignalRestaurantOrderAccepted)

		assert.Equal(t, expected, s.p.NextDeadline())
	})

	t.Run("courier facts before the delivery is confirmed", func(t *testing.T) {
		s := newProcess(t)
		s.run()
		s.signal(fulfillment.SignalRestaurantOrderReady)
		s.signal(fulfillment.SignalCourierAssignedToDelivery)
		s.signal(fulfillment.SignalDeliveryPickedUp)
		s.signal(fulfillment.SignalDeliveryCompleted)
		assert.False(t, s.p.Completed())

		s.signal(fulfillment.SignalRestaurantOrderAccepted)
		s.run()

		assert.True(t, s.p.Completed())
		assert.False(t, s.p.Failed())
		assert.Equal(t, start, s.p.PickedUpAt())
	})

	t.Run("completion before pickup does not time the delivery out", func(t *testing.T) {
		s := newProcess(t)
		s.accepted()
		s.signal(fulfillment.SignalCourierAssignedToDelivery)
		s.signal(fulfillment.SignalRestaurantOrderReady)

		assert.Equal(t, fulfillment.Remembered, s.signal(fulfillment.SignalDeliveryCompleted).Verdict)
		s.signal(fulfillment.SignalDeliveryPickedUp)
		s.run()

		assert.True(t, s.p.Completed())
	})

	t.Run("delivery failure before the delivery is confirmed", func(t *testing.T) {
		s := newProcess(t)
		s.run()
		assert.Equal(t, fulfillment.Remembered, s.signal(fulfillment.SignalDeliveryFailed).Verdict)
		assert.False(t, s.p.Failed())

		s.signal(fulfillment.SignalRestaurantOrderAccepted)
		s.run()

		require.True(t, s.p.Failed())
		_, code := s.outcome()
		assert.Equal(t, fulfillment.ReasonDeliveryFailed, code)
	})

	t.Run("a repeat of a remembered signal is a duplicate", func(t *testing.T) {
		s := newProcess(t)
		s.run()
		ready := fulfillment.Signal{Kind: fulfillment.SignalRestaurantOrderReady, EventID: "ready"}

		assert.Equal(t, fulfillment.Remembered, s.p.Apply(ready, s.now).Verdict)
		assert.Equal(t, fulfillment.Duplicate, s.p.Apply(ready, s.now).Verdict)
	})

	t.Run("a chosen outcome is not changed by a remembered signal", func(t *testing.T) {
		s := newProcess(t)
		s.run()
		s.signal(fulfillment.SignalRestaurantOrderReady)
		s.signal(fulfillment.SignalRestaurantOrderRejected)

		_, code := s.outcome()
		assert.Equal(t, fulfillment.ReasonRestaurantRejected, code)
	})
}

// Signals closed by their owner choose the negative outcome.
func TestProcessFailsOnOwnerSignals(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*process)
		signal  fulfillment.SignalKind
		reason  string
	}{
		"rejected before acceptance": {arrange: func(s *process) { s.run() }, signal: fulfillment.SignalRestaurantOrderRejected, reason: fulfillment.ReasonRestaurantRejected},
		"rejected after acceptance":  {arrange: func(s *process) { s.accepted() }, signal: fulfillment.SignalRestaurantOrderRejected, reason: fulfillment.ReasonRestaurantRejected},
		"preparation failed":         {arrange: func(s *process) { s.accepted() }, signal: fulfillment.SignalPreparationFailed, reason: fulfillment.ReasonPreparationFailed},
		"delivery failed":            {arrange: func(s *process) { s.pickedUp() }, signal: fulfillment.SignalDeliveryFailed, reason: fulfillment.ReasonDeliveryFailed},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			tc.arrange(s)

			effect := s.signal(tc.signal)

			assert.True(t, effect.Changed())
			require.True(t, s.p.Failed())
			outcome, code := s.outcome()
			assert.Equal(t, fulfillment.OutcomeFailure, outcome)
			assert.Equal(t, tc.reason, code)
		})
	}
}

// AC-440: a repeated event id does not apply a signal twice, and a signal without
// id cannot be deduplicated.
func TestProcessDeduplicatesSignals(t *testing.T) {
	s := newProcess(t)
	s.run()
	accepted := fulfillment.Signal{Kind: fulfillment.SignalRestaurantOrderAccepted, EventID: "event-1"}

	assert.Equal(t, fulfillment.Applied, s.p.Apply(accepted, s.now).Verdict)
	assert.Equal(t, fulfillment.Duplicate, s.p.Apply(accepted, s.now).Verdict)
	assert.Equal(t, fulfillment.Duplicate, s.p.Apply(fulfillment.Signal{Kind: fulfillment.SignalPreparationFailed, EventID: "event-1"}, s.now).Verdict)
	assert.Equal(t, fulfillment.MissingEventID, s.p.Apply(fulfillment.Signal{Kind: fulfillment.SignalPreparationFailed}, s.now).Verdict)
	assert.False(t, s.p.Failed())

	before := len(s.steps)
	s.run()
	assert.Equal(t, []fulfillment.StepKind{fulfillment.StepCreateDelivery}, kindsOf(s.steps[before:]), "one Delivery per Acceptance")
}

// Late signals after the outcome is chosen do not change the route.
func TestProcessIgnoresSignalsAfterOutcome(t *testing.T) {
	for name, arrange := range map[string]func(*process){
		"failed": func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalPreparationFailed)
		},
		"delivered": func(s *process) {
			s.pickedUp()
			s.signal(fulfillment.SignalDeliveryCompleted)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			arrange(s)
			outcome, code := s.outcome()

			effect := s.signal(fulfillment.SignalDeliveryFailed)

			assert.Equal(t, fulfillment.Closed, effect.Verdict)
			gotOutcome, gotCode := s.outcome()
			assert.Equal(t, outcome, gotOutcome)
			assert.Equal(t, code, gotCode)
		})
	}
}

// The confirmation of OrderPlaced is admitted without effect.
func TestProcessAdmitsOrderPlaced(t *testing.T) {
	s := newProcess(t)

	assert.True(t, s.signal(fulfillment.SignalOrderPlaced).Changed())
	assert.Equal(t, fulfillment.StageAuthorizingPayment, s.p.Stage())
	assert.False(t, s.p.Failed())
}
