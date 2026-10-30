package fulfillment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// Signals the current state does not admit are refused with a diagnostic reason
// and change nothing; AC-441: readiness before Acceptance is one of them.
func TestProcessRefusesSignalsOutOfOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*process)
		signal  fulfillment.SignalKind
		reason  string
	}{
		"accepted before request":   {arrange: func(*process) {}, signal: fulfillment.SignalRestaurantOrderAccepted, reason: "restaurant order not requested"},
		"accepted twice":            {arrange: func(s *process) { s.accepted() }, signal: fulfillment.SignalRestaurantOrderAccepted, reason: "already accepted"},
		"started before acceptance": {arrange: func(s *process) { s.run() }, signal: fulfillment.SignalPreparationStarted, reason: "not accepted"},
		"delayed before acceptance": {arrange: func(s *process) { s.run() }, signal: fulfillment.SignalPreparationDelayed, reason: "not preparing"},
		"ready before acceptance":   {arrange: func(s *process) { s.run() }, signal: fulfillment.SignalRestaurantOrderReady, reason: "not accepted"},
		"ready twice": {arrange: func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalRestaurantOrderReady)
		}, signal: fulfillment.SignalRestaurantOrderReady, reason: "already ready"},
		"preparation failed after ready": {arrange: func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalRestaurantOrderReady)
		}, signal: fulfillment.SignalPreparationFailed, reason: "already ready"},
		"assigned before delivery": {arrange: func(s *process) { s.run() }, signal: fulfillment.SignalCourierAssignedToDelivery, reason: "delivery not created"},
		"assigned twice": {arrange: func(s *process) {
			s.accepted()
			s.signal(fulfillment.SignalCourierAssignedToDelivery)
		}, signal: fulfillment.SignalCourierAssignedToDelivery, reason: "already assigned"},
		"picked up before assignment": {arrange: func(s *process) { s.accepted() }, signal: fulfillment.SignalDeliveryPickedUp, reason: "courier not assigned"},
		"picked up twice":             {arrange: func(s *process) { s.pickedUp() }, signal: fulfillment.SignalDeliveryPickedUp, reason: "already picked up"},
		"delivered before pickup":     {arrange: func(s *process) { s.accepted() }, signal: fulfillment.SignalDeliveryCompleted, reason: "not picked up"},
		"delivery failed before creation": {arrange: func(s *process) { s.run() }, signal: fulfillment.SignalDeliveryFailed,
			reason: "delivery not created"},
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
