package fulfillment_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// Facts the current state does not admit are refused with a diagnostic reason
// and change nothing; AC-441: readiness before Acceptance is one of them.
func TestProcessRefusesFactsOutOfOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*process)
		fact    fulfillment.FactKind
		reason  string
	}{
		"accepted before request":   {arrange: func(*process) {}, fact: fulfillment.FactRestaurantOrderAccepted, reason: "restaurant order not requested"},
		"accepted twice":            {arrange: func(s *process) { s.accepted() }, fact: fulfillment.FactRestaurantOrderAccepted, reason: "already accepted"},
		"started before acceptance": {arrange: func(s *process) { s.run() }, fact: fulfillment.FactPreparationStarted, reason: "not accepted"},
		"delayed before acceptance": {arrange: func(s *process) { s.run() }, fact: fulfillment.FactPreparationDelayed, reason: "not preparing"},
		"ready before acceptance":   {arrange: func(s *process) { s.run() }, fact: fulfillment.FactRestaurantOrderReady, reason: "not accepted"},
		"ready twice": {arrange: func(s *process) {
			s.accepted()
			s.fact(fulfillment.FactRestaurantOrderReady)
		}, fact: fulfillment.FactRestaurantOrderReady, reason: "already ready"},
		"preparation failed after ready": {arrange: func(s *process) {
			s.accepted()
			s.fact(fulfillment.FactRestaurantOrderReady)
		}, fact: fulfillment.FactPreparationFailed, reason: "already ready"},
		"assigned before delivery": {arrange: func(s *process) { s.run() }, fact: fulfillment.FactCourierAssignedToDelivery, reason: "delivery not created"},
		"assigned twice": {arrange: func(s *process) {
			s.accepted()
			s.fact(fulfillment.FactCourierAssignedToDelivery)
		}, fact: fulfillment.FactCourierAssignedToDelivery, reason: "already assigned"},
		"picked up before assignment": {arrange: func(s *process) { s.accepted() }, fact: fulfillment.FactDeliveryPickedUp, reason: "courier not assigned"},
		"picked up twice":             {arrange: func(s *process) { s.pickedUp() }, fact: fulfillment.FactDeliveryPickedUp, reason: "already picked up"},
		"delivered before pickup":     {arrange: func(s *process) { s.accepted() }, fact: fulfillment.FactDeliveryCompleted, reason: "not picked up"},
		"delivery failed before creation": {arrange: func(s *process) { s.run() }, fact: fulfillment.FactDeliveryFailed,
			reason: "delivery not created"},
		"rejected after pickup": {arrange: func(s *process) { s.pickedUp() }, fact: fulfillment.FactRestaurantOrderRejected, reason: "rejected after pickup"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			tc.arrange(s)
			stage, deadline := s.p.Stage(), s.p.NextDeadline()

			effect := s.fact(tc.fact)

			assert.Equal(t, fulfillment.Effect{Verdict: fulfillment.Refused, Reason: tc.reason}, effect)
			assert.False(t, effect.Changed())
			assert.Equal(t, stage, s.p.Stage())
			assert.Equal(t, deadline, s.p.NextDeadline())
			assert.False(t, s.p.Failed())
		})
	}
}

// Facts closed by their owner choose the negative outcome.
func TestProcessFailsOnOwnerFacts(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*process)
		fact    fulfillment.FactKind
		reason  string
	}{
		"rejected before acceptance": {arrange: func(s *process) { s.run() }, fact: fulfillment.FactRestaurantOrderRejected, reason: fulfillment.ReasonRestaurantRejected},
		"rejected after acceptance":  {arrange: func(s *process) { s.accepted() }, fact: fulfillment.FactRestaurantOrderRejected, reason: fulfillment.ReasonRestaurantRejected},
		"preparation failed":         {arrange: func(s *process) { s.accepted() }, fact: fulfillment.FactPreparationFailed, reason: fulfillment.ReasonPreparationFailed},
		"delivery failed":            {arrange: func(s *process) { s.pickedUp() }, fact: fulfillment.FactDeliveryFailed, reason: fulfillment.ReasonDeliveryFailed},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			tc.arrange(s)

			effect := s.fact(tc.fact)

			assert.True(t, effect.Changed())
			require.True(t, s.p.Failed())
			outcome, code := s.outcome()
			assert.Equal(t, fulfillment.OutcomeFailure, outcome)
			assert.Equal(t, tc.reason, code)
		})
	}
}

// AC-440: a repeated event id does not apply a fact twice; ids are shared with
// operator decisions, and a fact without id cannot be deduplicated.
func TestProcessDeduplicatesFacts(t *testing.T) {
	s := newProcess(t)
	s.run()
	accepted := fulfillment.Fact{Kind: fulfillment.FactRestaurantOrderAccepted, EventID: "event-1"}

	assert.Equal(t, fulfillment.Applied, s.p.Apply(accepted, s.now).Verdict)
	assert.Equal(t, fulfillment.Duplicate, s.p.Apply(accepted, s.now).Verdict)
	assert.Equal(t, fulfillment.Duplicate, s.p.Apply(fulfillment.Fact{Kind: fulfillment.FactPreparationFailed, EventID: "event-1"}, s.now).Verdict)
	assert.Equal(t, fulfillment.Duplicate, s.p.Decide(fulfillment.OperatorDecision{DecisionID: "event-1", Choice: fulfillment.OperatorCancel}).Verdict)
	assert.Equal(t, fulfillment.MissingEventID, s.p.Apply(fulfillment.Fact{Kind: fulfillment.FactPreparationFailed}, s.now).Verdict)
	assert.False(t, s.p.Failed())

	before := len(s.steps)
	s.run()
	assert.Equal(t, []fulfillment.StepKind{fulfillment.StepCreateDelivery}, kindsOf(s.steps[before:]), "one Delivery per Acceptance")
}

// Late facts after the outcome is chosen do not change the route.
func TestProcessIgnoresFactsAfterOutcome(t *testing.T) {
	for name, arrange := range map[string]func(*process){
		"failed": func(s *process) {
			s.accepted()
			s.fact(fulfillment.FactPreparationFailed)
		},
		"delivered": func(s *process) {
			s.pickedUp()
			s.fact(fulfillment.FactDeliveryCompleted)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			arrange(s)
			outcome, code := s.outcome()

			effect := s.fact(fulfillment.FactDeliveryFailed)

			assert.Equal(t, fulfillment.Closed, effect.Verdict)
			gotOutcome, gotCode := s.outcome()
			assert.Equal(t, outcome, gotOutcome)
			assert.Equal(t, code, gotCode)
		})
	}
}

// Confirmations of OrderPlaced and the payment result are admitted without effect.
func TestProcessAdmitsConfirmations(t *testing.T) {
	for _, kind := range []fulfillment.FactKind{fulfillment.FactOrderPlaced, fulfillment.FactPaymentAuthorized, fulfillment.FactPaymentFailed} {
		t.Run(string(kind), func(t *testing.T) {
			s := newProcess(t)

			assert.True(t, s.fact(kind).Changed())
			assert.Equal(t, fulfillment.StageAuthorizingPayment, s.p.Stage())
			assert.False(t, s.p.Failed())
		})
	}
}

// AC-437: the operator decides on an escalated assignment only.
func TestProcessAppliesOperatorDecision(t *testing.T) {
	escalate := func(s *process) {
		s.accepted()
		s.fact(fulfillment.FactRestaurantOrderReady)
		for s.expire() != fulfillment.ExpiryEscalation {
			s.run()
		}
	}
	t.Run("retry assignment replans again", func(t *testing.T) {
		s := newProcess(t)
		escalate(s)

		effect := s.p.Decide(fulfillment.OperatorDecision{DecisionID: "decision-1", Choice: fulfillment.OperatorRetryAssignment})

		assert.True(t, effect.Changed())
		step, ok := s.p.Next()
		require.True(t, ok)
		assert.Equal(t, fulfillment.Step{Kind: fulfillment.StepReplanDelivery, Key: "replan_delivery_3"}, step)
		s.p.Confirm(step, true, s.now)
		s.fact(fulfillment.FactCourierAssignedToDelivery)
		assert.Equal(t, fulfillment.StageWaitingPickup, s.p.Stage())
	})
	t.Run("cancel", func(t *testing.T) {
		s := newProcess(t)
		escalate(s)

		assert.True(t, s.p.Decide(fulfillment.OperatorDecision{DecisionID: "decision-1", Choice: fulfillment.OperatorCancel}).Changed())
		outcome, code := s.outcome()
		assert.Equal(t, fulfillment.OutcomeCancellation, outcome)
		assert.Equal(t, fulfillment.ReasonOperatorDecision, code)
	})
	t.Run("unknown choice", func(t *testing.T) {
		s := newProcess(t)
		escalate(s)

		effect := s.p.Decide(fulfillment.OperatorDecision{DecisionID: "decision-1", Choice: "wait"})

		assert.Equal(t, fulfillment.Effect{Verdict: fulfillment.Refused, Reason: `unknown choice "wait"`}, effect)
		assert.Equal(t, fulfillment.StageWaitingOperator, s.p.Stage())
	})
	t.Run("without escalation", func(t *testing.T) {
		s := newProcess(t).accepted()

		effect := s.p.Decide(fulfillment.OperatorDecision{DecisionID: "decision-1", Choice: fulfillment.OperatorCancel})

		assert.Equal(t, fulfillment.Effect{Verdict: fulfillment.Refused, Reason: "no escalation"}, effect)
		assert.False(t, s.p.Failed())
	})
}

// A courier assigned while waiting for the operator ends the escalation.
func TestProcessAssignmentEndsEscalation(t *testing.T) {
	s := newProcess(t).accepted()
	for s.expire() != fulfillment.ExpiryEscalation {
		s.run()
	}

	assert.True(t, s.fact(fulfillment.FactCourierAssignedToDelivery).Changed())
	assert.Equal(t, fulfillment.StagePreparing, s.p.Stage())
	assert.Equal(t, fulfillment.Refused, s.p.Decide(fulfillment.OperatorDecision{DecisionID: "late", Choice: fulfillment.OperatorCancel}).Verdict)
}
