package fulfillment_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// AC-546: with MaxReplans = 2 the model replans twice and escalates on the
// third expiry of the courier assignment deadline.
func TestProcessReplansAssignmentThenEscalates(t *testing.T) {
	s := newProcess(t).accepted()
	s.fact(fulfillment.FactRestaurantOrderReady)
	require.Equal(t, 2, fulfillment.DefaultPolicy().MaxReplans)

	var replans []fulfillment.Step
	for range 2 {
		require.Equal(t, fulfillment.ExpiryReplan, s.expire())
		step, ok := s.p.Next()
		require.True(t, ok)
		require.Equal(t, fulfillment.StepReplanDelivery, step.Kind)
		replans = append(replans, step)
		s.p.Confirm(step, true, s.now)
	}
	require.Equal(t, fulfillment.ExpiryEscalation, s.expire())
	escalatedAt := s.now

	assert.Equal(t, []string{"replan_delivery_1", "replan_delivery_2"}, keysOf(replans))
	assert.Equal(t, 2, s.p.Replans())
	assert.Equal(t, fulfillment.StageWaitingOperator, s.p.Stage())
	assert.False(t, s.p.Failed())
	_, ok := s.p.Next()
	assert.False(t, ok, "the escalated process waits for the operator")
	assert.Equal(t, escalatedAt.Add(fulfillment.DefaultPolicy().OperatorDecisionTimeout), s.p.NextDeadline())
}

// Every deadline of the Policy turns into its business branch.
func TestProcessExpiresDeadlines(t *testing.T) {
	assigned := func(s *process) {
		s.accepted()
		s.fact(fulfillment.FactCourierAssignedToDelivery)
	}
	escalated := func(s *process) {
		s.accepted()
		for s.expire() != fulfillment.ExpiryEscalation {
			s.run()
		}
	}
	for name, tc := range map[string]struct {
		arrange func(*process)
		timeout time.Duration
		reason  string
	}{
		// AC-435
		"acceptance": {arrange: func(s *process) { s.run() }, timeout: fulfillment.DefaultPolicy().AcceptanceTimeout, reason: fulfillment.ReasonRestaurantAcceptanceTimeout},
		"operator decision": {arrange: escalated, timeout: fulfillment.DefaultPolicy().OperatorDecisionTimeout,
			reason: fulfillment.ReasonCourierAssignmentExhausted},
		"preparation": {arrange: assigned, timeout: fulfillment.DefaultPolicy().PreparationTimeout, reason: fulfillment.ReasonPreparationTimeout},
		"pickup": {arrange: func(s *process) {
			assigned(s)
			s.fact(fulfillment.FactRestaurantOrderReady)
		}, timeout: fulfillment.DefaultPolicy().PickupTimeout, reason: fulfillment.ReasonPickupTimeout},
		"delivery": {arrange: func(s *process) { s.pickedUp() }, timeout: fulfillment.DefaultPolicy().DeliveryTimeout, reason: fulfillment.ReasonDeliveryTimeout},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t)
			tc.arrange(s)
			armedAt := s.now

			require.Equal(t, armedAt.Add(tc.timeout), s.p.NextDeadline())
			assert.Equal(t, fulfillment.ExpiryNone, s.p.Expire(s.p.NextDeadline().Add(-time.Nanosecond)), "not yet passed")
			assert.Equal(t, fulfillment.ExpiryFailure, s.expire())

			outcome, code := s.outcome()
			assert.Equal(t, fulfillment.OutcomeFailure, outcome)
			assert.Equal(t, tc.reason, code)
		})
	}
}

// AC-436: PreparationDelayed moves the Preparation deadline up to
// MaxPreparationExtension; an earlier estimate does not shorten it.
func TestProcessExtendsPreparationOnDelay(t *testing.T) {
	policy := fulfillment.DefaultPolicy()
	deadline := start.Add(policy.PreparationTimeout)
	limit := deadline.Add(policy.MaxPreparationExtension)
	for name, tc := range map[string]struct {
		expectedReadyAt time.Time
		verdict         fulfillment.Verdict
		reason          string
		want            time.Time
	}{
		"extends":      {expectedReadyAt: deadline.Add(20 * time.Minute), verdict: fulfillment.Applied, want: deadline.Add(20 * time.Minute)},
		"capped":       {expectedReadyAt: limit.Add(time.Hour), verdict: fulfillment.Applied, want: limit},
		"no extension": {expectedReadyAt: deadline.Add(-time.Minute), verdict: fulfillment.Refused, reason: "no extension", want: deadline},
	} {
		t.Run(name, func(t *testing.T) {
			s := newProcess(t).accepted()
			s.fact(fulfillment.FactCourierAssignedToDelivery)

			effect := s.p.Apply(fulfillment.Fact{Kind: fulfillment.FactPreparationDelayed, EventID: "delayed", ExpectedReadyAt: tc.expectedReadyAt}, s.now)

			assert.Equal(t, fulfillment.Effect{Verdict: tc.verdict, Reason: tc.reason}, effect)
			assert.Equal(t, tc.want, s.p.NextDeadline())
		})
	}
}

func TestProcessNextDeadlineIsNearestActive(t *testing.T) {
	s := newProcess(t)
	assert.True(t, s.p.NextDeadline().IsZero(), "no deadline before the restaurant request")

	s.at(time.Minute).accepted()

	policy := fulfillment.DefaultPolicy()
	assert.Equal(t, s.now.Add(min(policy.AssignmentTimeout, policy.PreparationTimeout)), s.p.NextDeadline())
}
