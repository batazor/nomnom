package fulfillment_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

// scheduledProcess drives a Scheduled Order: plan steps take the plans the
// test gives, in order.
type scheduledProcess struct {
	*process
	plans []fulfillment.Plan
}

// windowPlan is the plan of AC-906 for the window 19:00–19:15 of the day of
// start: planned ready 18:40, start 18:10, courier 18:25, replan 17:40.
func windowPlan() fulfillment.Plan {
	day := start.Truncate(24 * time.Hour)
	return fulfillment.Plan{
		PlannedReadyAt:   day.Add(18*time.Hour + 40*time.Minute),
		StartAt:          day.Add(18*time.Hour + 10*time.Minute),
		CourierRequestAt: day.Add(18*time.Hour + 25*time.Minute),
		ReplanAt:         day.Add(17*time.Hour + 40*time.Minute),
	}
}

func newScheduledProcess(t *testing.T, plans ...fulfillment.Plan) *scheduledProcess {
	t.Helper()
	s := newProcess(t)
	s.p = fulfillment.NewScheduled(fulfillment.DefaultPolicy())
	return &scheduledProcess{process: s, plans: plans}
}

// run executes every due step; a plan step confirms the next plan of the test.
func (s *scheduledProcess) run() *scheduledProcess {
	for {
		step, ok := s.p.Next()
		if !ok {
			return s
		}
		s.steps = append(s.steps, step)
		if step.Kind == fulfillment.StepPlanSchedule || step.Kind == fulfillment.StepReplanSchedule {
			require.NotEmpty(s.t, s.plans, "no plan left for %s", step.Kind)
			s.p.ConfirmPlan(step, s.plans[0], s.now)
			s.plans = s.plans[1:]
			continue
		}
		s.p.Confirm(step, !s.declined[step.Kind], s.now)
	}
}

// until expires the moments of the plan up to the given time and runs the
// steps they make due.
func (s *scheduledProcess) until(moment time.Time) *scheduledProcess {
	for {
		next := s.p.NextDeadline()
		if next.IsZero() || next.After(moment) {
			s.now = moment
			return s
		}
		if next.After(s.now) {
			s.now = next
		}
		s.p.Expire(s.now)
		s.run()
	}
}

func (s *scheduledProcess) accepted() *scheduledProcess {
	s.run()
	s.fact(fulfillment.FactRestaurantOrderAccepted)
	return s.run()
}

// AC-917, FR-678: the scheduled route confirms the slot and plans before the
// restaurant, waits in stage scheduled and starts the kitchen and the courier
// only at their moments, then follows the ASAP route.
func TestScheduledProcessRunsTheScheduledRoute(t *testing.T) {
	s := newScheduledProcess(t, windowPlan(), windowPlan())
	s.now = windowPlan().ReplanAt.Add(-3 * time.Hour)
	s.accepted()

	assert.Equal(t, []fulfillment.StepKind{
		fulfillment.StepAuthorizePayment, fulfillment.StepRecordPaymentAuthorization,
		fulfillment.StepConfirmSlotReservation, fulfillment.StepPlanSchedule, fulfillment.StepRequestRestaurantOrder,
	}, kindsOf(s.steps), "nothing more runs right after Acceptance")
	assert.Equal(t, fulfillment.StageScheduled, s.p.Stage())
	assert.Equal(t, windowPlan().ReplanAt, s.p.NextDeadline(), "the replan checkpoint is the first timer")

	s.until(windowPlan().StartAt.Add(-time.Second))
	assert.Equal(t, fulfillment.StepReplanSchedule, s.steps[len(s.steps)-1].Kind)
	assert.Equal(t, fulfillment.StageScheduled, s.p.Stage())

	s.until(windowPlan().StartAt)
	assert.Equal(t, fulfillment.StepQueueRestaurantOrder, s.steps[len(s.steps)-1].Kind)
	assert.Equal(t, "queue", s.steps[len(s.steps)-1].Key, "idempotency key order_id + queue")
	assert.Equal(t, fulfillment.StagePreparing, s.p.Stage())

	s.until(windowPlan().CourierRequestAt)
	assert.Equal(t, fulfillment.StepCreateDelivery, s.steps[len(s.steps)-1].Kind)

	s.fact(fulfillment.FactCourierAssignedToDelivery)
	s.fact(fulfillment.FactRestaurantOrderReady)
	s.fact(fulfillment.FactDeliveryPickedUp)
	s.fact(fulfillment.FactDeliveryCompleted)
	s.run()
	assert.Equal(t, []string{
		"authorize_payment", "record_payment_authorization", "confirm_slot", "plan_schedule", "request_restaurant_order",
		"replan_schedule", "queue", "create_delivery", "capture_payment", "complete_order",
	}, keysOf(s.steps))
	assert.True(t, s.p.Completed())
}

// AC-918: the kitchen accepted three hours before start_at; no Preparation
// deadline runs in stage scheduled, it counts from QueueRestaurantOrder.
func TestScheduledProcessArmsPreparationAtQueue(t *testing.T) {
	plan := windowPlan()
	s := newScheduledProcess(t, plan, plan)
	s.now = plan.StartAt.Add(-3 * time.Hour)
	s.accepted()

	s.until(plan.StartAt.Add(-time.Minute))
	outcome, _ := s.outcome()
	assert.Empty(t, outcome, "longer than the Preparation timeout passed without a compensation")
	assert.Equal(t, fulfillment.StageScheduled, s.p.Stage())

	s.until(plan.StartAt)
	require.Equal(t, fulfillment.StepQueueRestaurantOrder, s.steps[len(s.steps)-1].Kind)
	assert.Equal(t, plan.CourierRequestAt, s.p.NextDeadline())
	s.until(plan.CourierRequestAt)
	// The courier assignment has no deadline of its own: the nearest one is
	// still the Preparation armed at QueueRestaurantOrder.
	assert.Equal(t, plan.StartAt.Add(fulfillment.DefaultPolicy().PreparationTimeout), s.p.NextDeadline(),
		"the courier assignment has no deadline")
	s.fact(fulfillment.FactCourierAssignedToDelivery)
	assert.Equal(t, plan.StartAt.Add(fulfillment.DefaultPolicy().PreparationTimeout), s.p.NextDeadline(),
		"Preparation counts from QueueRestaurantOrder")
}

// AC-911: no Delivery before courier_request_at.
func TestScheduledProcessRequestsTheCourierAtItsMoment(t *testing.T) {
	plan := windowPlan()
	s := newScheduledProcess(t, plan, plan)
	s.now = plan.StartAt.Add(-3 * time.Hour)
	s.accepted()

	s.until(plan.CourierRequestAt.Add(-time.Second))
	assert.NotContains(t, kindsOf(s.steps), fulfillment.StepCreateDelivery)
	s.until(plan.CourierRequestAt)
	assert.Contains(t, kindsOf(s.steps), fulfillment.StepCreateDelivery)
}

// AC-908: the kitchen accepted after courier_request_at: every passed step
// runs at once, without a timer.
func TestScheduledProcessRunsPassedMomentsAtOnce(t *testing.T) {
	plan := windowPlan()
	s := newScheduledProcess(t, plan, plan)
	s.now = plan.CourierRequestAt.Add(time.Minute)
	s.accepted()

	s.until(s.now)
	assert.Equal(t, []fulfillment.StepKind{
		fulfillment.StepAuthorizePayment, fulfillment.StepRecordPaymentAuthorization,
		fulfillment.StepConfirmSlotReservation, fulfillment.StepPlanSchedule, fulfillment.StepRequestRestaurantOrder,
		fulfillment.StepQueueRestaurantOrder, fulfillment.StepCreateDelivery,
	}, kindsOf(s.steps), "a first plan made after its checkpoint is not replanned")
}

// AC-909, AC-910: the replan keeps planned_ready_at; a new start_at already in
// the past queues the order at once.
func TestScheduledProcessReplanMovesTheStart(t *testing.T) {
	plan := windowPlan()
	replanned := plan
	replanned.StartAt = plan.ReplanAt.Add(-5 * time.Minute)
	replanned.CourierRequestAt = plan.CourierRequestAt
	s := newScheduledProcess(t, plan, replanned)
	s.now = plan.ReplanAt.Add(-time.Hour)
	s.accepted()

	s.until(plan.ReplanAt)

	assert.Equal(t, plan.PlannedReadyAt, s.p.Plan().PlannedReadyAt, "planned_ready_at does not move")
	assert.Equal(t, replanned.StartAt, s.p.Plan().StartAt)
	kinds := kindsOf(s.steps)
	assert.Equal(t, []fulfillment.StepKind{fulfillment.StepReplanSchedule, fulfillment.StepQueueRestaurantOrder}, kinds[len(kinds)-2:])
	assert.Equal(t, plan.ReplanAt, s.now)
}

// The scheduled route compensates the slot reservation until Pickup.
func TestScheduledProcessPlansCompensation(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*scheduledProcess)
		outcome fulfillment.Outcome
		reason  string
		saga    []fulfillment.StepKind
	}{
		// AC-919
		"payment declined": {
			arrange: func(s *scheduledProcess) {
				s.declined[fulfillment.StepAuthorizePayment] = true
				s.run()
			},
			outcome: fulfillment.OutcomeFailure, reason: fulfillment.ReasonPaymentDeclined,
			saga: []fulfillment.StepKind{fulfillment.StepReleaseSlotReservation, fulfillment.StepFailOrCancelOrder},
		},
		// FR-682: the hold expired and the window filled up.
		"slot unavailable at confirmation": {
			arrange: func(s *scheduledProcess) {
				s.declined[fulfillment.StepConfirmSlotReservation] = true
				s.run()
			},
			// Without a restaurant request: nothing to cancel in restaurants.
			outcome: fulfillment.OutcomeFailure, reason: fulfillment.ReasonSlotUnavailable,
			saga: []fulfillment.StepKind{fulfillment.StepCancelPaymentAuthorization, fulfillment.StepFailOrCancelOrder},
		},
		// AC-920
		"cancellation in stage scheduled": {
			arrange: func(s *scheduledProcess) {
				s.accepted()
				require.Equal(s.t, fulfillment.StageScheduled, s.p.Stage())
				require.NoError(s.t, s.p.RequestCancellation(fulfillment.CancellationRequest{RequestID: "request-1"}))
			},
			outcome: fulfillment.OutcomeCancellation, reason: fulfillment.ReasonCustomerRequested,
			saga: []fulfillment.StepKind{
				fulfillment.StepCancelRestaurantOrder, fulfillment.StepReleaseSlotReservation,
				fulfillment.StepCancelPaymentAuthorization, fulfillment.StepFailOrCancelOrder,
			},
		},
		"delivery failed after pickup keeps the window": {
			arrange: func(s *scheduledProcess) {
				s.now = windowPlan().CourierRequestAt
				s.accepted()
				s.until(s.now)
				s.fact(fulfillment.FactCourierAssignedToDelivery)
				s.fact(fulfillment.FactRestaurantOrderReady)
				s.fact(fulfillment.FactDeliveryPickedUp)
				s.run()
				s.fact(fulfillment.FactDeliveryFailed)
			},
			outcome: fulfillment.OutcomeFailure, reason: fulfillment.ReasonDeliveryFailed,
			saga: []fulfillment.StepKind{fulfillment.StepRefundPayment, fulfillment.StepFailOrCancelOrder},
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newScheduledProcess(t, windowPlan(), windowPlan())
			s.now = windowPlan().ReplanAt.Add(-time.Hour)
			tc.arrange(s)

			require.True(t, s.p.Failed())
			outcome, reason := s.outcome()
			assert.Equal(t, tc.outcome, outcome)
			assert.Equal(t, tc.reason, reason)
			assert.Equal(t, tc.saga, kindsOf(s.p.Compensate()))
		})
	}
}

// PreparationDelayed before QueueRestaurantOrder has no deadline to extend.
func TestScheduledProcessRefusesDelayBeforeQueue(t *testing.T) {
	s := newScheduledProcess(t, windowPlan(), windowPlan())
	s.now = windowPlan().ReplanAt.Add(-time.Hour)
	s.accepted()

	effect := s.p.Apply(fulfillment.Fact{Kind: fulfillment.FactPreparationDelayed, EventID: "delay-1", ExpectedReadyAt: windowPlan().PlannedReadyAt}, s.now)

	assert.Equal(t, fulfillment.Refused, effect.Verdict)
	assert.Equal(t, "not queued", effect.Reason)
}
