package fulfillment_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// AC-968, AC-969: the courier assignment has no deadline; a day without a
// courier yields neither a step nor a deadline, and the process keeps
// waiting for CourierAssignedToDelivery.
func TestProcessWaitsForCourierWithoutDeadline(t *testing.T) {
	s := newProcess(t).accepted()
	s.fact(fulfillment.FactRestaurantOrderReady)

	assert.True(t, s.p.NextDeadline().IsZero())
	assert.Equal(t, fulfillment.ExpiryNone, s.p.Expire(s.now.Add(24*time.Hour)))
	_, ok := s.p.Next()
	assert.False(t, ok)
	assert.False(t, s.p.Failed())
	assert.Equal(t, fulfillment.StagePreparing, s.p.Stage())

	s.fact(fulfillment.FactCourierAssignedToDelivery)
	assert.Equal(t, fulfillment.StageWaitingPickup, s.p.Stage())
}

// Every deadline of the Policy turns into its business branch.
func TestProcessExpiresDeadlines(t *testing.T) {
	assigned := func(s *process) {
		s.accepted()
		s.fact(fulfillment.FactCourierAssignedToDelivery)
	}
	for name, tc := range map[string]struct {
		arrange func(*process)
		timeout time.Duration
		reason  string
	}{
		// AC-435
		"acceptance":  {arrange: func(s *process) { s.run() }, timeout: fulfillment.DefaultPolicy().AcceptanceTimeout, reason: fulfillment.ReasonRestaurantAcceptanceTimeout},
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
	assert.Equal(t, s.now.Add(policy.PreparationTimeout), s.p.NextDeadline())
}
