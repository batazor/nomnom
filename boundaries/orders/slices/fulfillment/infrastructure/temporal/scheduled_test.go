package temporal_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
)

// startedAt is the workflow start of the scheduled tests; the window is 19:00
// of the same day.
var (
	startedAt   = time.Date(2026, time.October, 25, 12, 0, 0, 0, time.UTC)
	windowStart = time.Date(2026, time.October, 25, 19, 0, 0, 0, time.UTC)
)

// windowPlan is the plan of AC-906 for the window 19:00–19:15.
func windowPlan() temporal.Plan {
	return temporal.Plan{
		PlannedReadyAt:   windowStart.Add(-20 * time.Minute),
		StartAt:          windowStart.Add(-50 * time.Minute),
		CourierRequestAt: windowStart.Add(-35 * time.Minute),
		ReplanAt:         windowStart.Add(-80 * time.Minute),
	}
}

// scheduled extends the harness with the Activities of the scheduled route:
// PlanSchedule returns the plans of the test in order, ConfirmSlotReservation
// confirms unless the slot is taken; every call records the workflow time.
type scheduled struct {
	*harness
	plans    []temporal.Plan
	slotFull bool
	at       map[string][]time.Time
}

func newScheduled(t *testing.T, plans ...temporal.Plan) *scheduled {
	t.Helper()
	s := &scheduled{harness: newHarness(t), plans: plans, at: map[string][]time.Time{}}
	s.env.SetStartTime(startedAt)
	s.env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		s.at[info.ActivityType.Name] = append(s.at[info.ActivityType.Name], s.env.Now().UTC())
	})
	s.env.OnActivity(temporal.ActivityConfirmSlotReservation, mock.Anything, mock.Anything).
		Return(func(context.Context, temporal.StepInput) (temporal.SlotDecision, error) {
			return temporal.SlotDecision{Confirmed: !s.slotFull}, nil
		}).
		Run(s.call(temporal.ActivityConfirmSlotReservation))
	s.env.OnActivity(temporal.ActivityPlanSchedule, mock.Anything, mock.Anything).
		Return(func(context.Context, temporal.StepInput) (temporal.Plan, error) {
			require.NotEmpty(t, s.plans, "no plan left")
			plan := s.plans[0]
			s.plans = s.plans[1:]
			return plan, nil
		}).
		Run(s.call(temporal.ActivityPlanSchedule))
	for _, name := range []string{temporal.ActivityQueueRestaurantOrder, temporal.ActivityReleaseSlotReservation} {
		s.env.OnActivity(name, mock.Anything, mock.Anything).Return(nil).Run(s.call(name))
	}
	return s
}

// call records a call of an Activity of the scheduled route like the harness.
func (s *scheduled) call(name string) func(mock.Arguments) {
	return func(args mock.Arguments) {
		s.calls = append(s.calls, name)
		if step, ok := args.Get(1).(temporal.StepInput); ok {
			s.steps = append(s.steps, step)
		}
	}
}

func (s *scheduled) run(policy temporal.Policy) temporal.Result {
	s.t.Helper()
	s.env.ExecuteWorkflow(temporal.WorkflowName, temporal.Input{
		OrderID:        orderID,
		Policy:         policy,
		DeliveryWindow: &temporal.DeliveryWindow{Start: windowStart, End: windowStart.Add(15 * time.Minute)},
	})
	require.True(s.t, s.env.IsWorkflowCompleted())
	require.NoError(s.t, s.env.GetWorkflowError())
	var result temporal.Result
	require.NoError(s.t, s.env.GetWorkflowResult(&result))
	return result
}

func (s *scheduled) signalAtTime(at time.Time, name, eventID string) {
	s.signalAt(at.Sub(startedAt), name, eventID)
}

func (s *scheduled) stepOf(key string) temporal.StepInput {
	s.t.Helper()
	for _, step := range s.steps {
		if step.IdempotencyKey == "order:order-1:"+key {
			return step
		}
	}
	s.t.Fatalf("no step %s in %v", key, s.steps)
	return temporal.StepInput{}
}

// deliverAfterCourier completes the Order once the courier was requested.
func (s *scheduled) deliverAfterCourier() {
	plan := windowPlan()
	s.signalAtTime(plan.CourierRequestAt.Add(5*time.Minute), temporal.SignalCourierAssignedToDelivery, "assigned")
	s.signalAtTime(plan.PlannedReadyAt, temporal.SignalRestaurantOrderReady, "ready")
	s.signalAtTime(plan.PlannedReadyAt.Add(5*time.Minute), temporal.SignalDeliveryPickedUp, "picked-up")
	s.signalAtTime(windowStart.Add(5*time.Minute), temporal.SignalDeliveryCompleted, "delivered")
}

// AC-917, AC-907, AC-911, AC-918: the kitchen accepts hours ahead; the
// workflow confirms the slot, plans, requests the restaurant with the window,
// replans at the checkpoint, queues the kitchen at start_at and requests the
// courier at courier_request_at, then completes on the ASAP route. Waiting
// longer than the Preparation deadline in stage scheduled compensates nothing.
func TestWorkflowRunsTheScheduledRoute(t *testing.T) {
	s := newScheduled(t, windowPlan(), windowPlan())
	s.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	s.deliverAfterCourier()

	result := s.run(policy())

	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeCompleted}, result)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityConfirmSlotReservation,
		temporal.ActivityPlanSchedule,
		temporal.ActivityRequestRestaurantOrder,
		temporal.ActivityPlanSchedule,
		temporal.ActivityQueueRestaurantOrder,
		temporal.ActivityCreateDelivery,
		temporal.ActivityCapturePayment,
		temporal.ActivityCompleteOrder,
	}, s.calls)
	plan := windowPlan()
	assert.Equal(t, plan.PlannedReadyAt, s.stepOf("request_restaurant_order").PlannedReadyAt, "the restaurant gets planned_ready_at")
	assert.True(t, s.stepOf("plan_schedule").PlannedReadyAt.IsZero(), "the first plan computes planned_ready_at")
	assert.Equal(t, plan.PlannedReadyAt, s.stepOf("replan_schedule").PlannedReadyAt, "the replan keeps planned_ready_at")
	assert.Equal(t, "ro-1", s.stepOf("queue").RestaurantOrderID)
	assert.Equal(t, []time.Time{plan.StartAt}, s.at[temporal.ActivityQueueRestaurantOrder], "QueueRestaurantOrder at start_at")
	assert.Equal(t, []time.Time{plan.CourierRequestAt}, s.at[temporal.ActivityCreateDelivery], "CreateDelivery at courier_request_at")
	assert.Equal(t, []time.Time{startedAt, plan.ReplanAt}, s.at[temporal.ActivityPlanSchedule])
}

// AC-908: the kitchen accepted after courier_request_at: the passed steps run
// at once, without timers.
func TestWorkflowRunsPassedMomentsAtOnce(t *testing.T) {
	past := temporal.Plan{
		PlannedReadyAt:   startedAt.Add(20 * time.Minute),
		StartAt:          startedAt.Add(-10 * time.Minute),
		CourierRequestAt: startedAt.Add(5 * time.Minute),
		ReplanAt:         startedAt.Add(-40 * time.Minute),
	}
	s := newScheduled(t, past)
	accepted := startedAt.Add(6 * time.Minute)
	s.signalAtTime(accepted, temporal.SignalRestaurantOrderAccepted, "accepted")
	s.signalAtTime(accepted.Add(time.Minute), temporal.SignalCourierAssignedToDelivery, "assigned")
	s.signalAtTime(accepted.Add(2*time.Minute), temporal.SignalRestaurantOrderReady, "ready")
	s.signalAtTime(accepted.Add(3*time.Minute), temporal.SignalDeliveryPickedUp, "picked-up")
	s.signalAtTime(accepted.Add(4*time.Minute), temporal.SignalDeliveryCompleted, "delivered")

	result := s.run(policy())

	assert.Equal(t, temporal.OutcomeCompleted, result.Outcome)
	assert.Equal(t, []time.Time{accepted}, s.at[temporal.ActivityQueueRestaurantOrder])
	assert.Equal(t, []time.Time{accepted}, s.at[temporal.ActivityCreateDelivery])
	assert.Len(t, s.at[temporal.ActivityPlanSchedule], 1, "a plan made after its checkpoint is not replanned")
}

// AC-909, AC-910: the checkpoint moves start_at earlier, into the past: the
// kitchen is queued at once, planned_ready_at stays.
func TestWorkflowReplanQueuesAtOnceWhenTheNewStartPassed(t *testing.T) {
	plan := windowPlan()
	replanned := plan
	replanned.StartAt = plan.ReplanAt.Add(-10 * time.Minute)
	s := newScheduled(t, plan, replanned)
	s.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	s.deliverAfterCourier()

	result := s.run(policy())

	assert.Equal(t, temporal.OutcomeCompleted, result.Outcome)
	assert.Equal(t, []time.Time{plan.ReplanAt}, s.at[temporal.ActivityQueueRestaurantOrder])
	assert.Equal(t, []time.Time{plan.CourierRequestAt}, s.at[temporal.ActivityCreateDelivery])
}

// AC-919: a declined Authorization releases the reservation and ends the Order.
func TestWorkflowReleasesSlotOnDeclinedPayment(t *testing.T) {
	s := newScheduled(t)
	s.declinePayment(temporal.ActivityAuthorizePayment)

	result := s.run(policy())

	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeFailure, ReasonCode: temporal.ReasonPaymentDeclined}, result)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityReleaseSlotReservation,
		temporal.ActivityFailOrCancelOrder,
	}, s.calls)
	assert.Equal(t, "order:order-1:release_slot", s.steps[1].IdempotencyKey)
}

// FR-682: SLOT_UNAVAILABLE at confirmation cancels the Authorization and ends
// the Order with slot_unavailable without requesting the restaurant.
func TestWorkflowEndsOrderWhenTheSlotIsGone(t *testing.T) {
	s := newScheduled(t)
	s.slotFull = true

	result := s.run(policy())

	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeFailure, ReasonCode: temporal.ReasonSlotUnavailable}, result)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityConfirmSlotReservation,
		temporal.ActivityCancelPaymentAuthorization,
		temporal.ActivityFailOrCancelOrder,
	}, s.calls)
	assert.Equal(t, temporal.ReasonSlotUnavailable, s.finalize.ReasonCode)
}

// AC-920: a Cancellation in stage scheduled cancels the Restaurant Order,
// releases the reservation and cancels the Authorization.
func TestWorkflowCancelsInStageScheduled(t *testing.T) {
	s := newScheduled(t, windowPlan())
	s.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	var stages []string
	s.env.OnUpsertTypedSearchAttributes(mock.Anything).Run(func(args mock.Arguments) {
		stage, ok := args.Get(0).(sdktemporal.SearchAttributes).GetKeyword(temporal.StageSearchAttribute)
		require.True(t, ok)
		stages = append(stages, stage)
	}).Return(nil)
	s.env.RegisterDelayedCallback(func() {
		s.env.UpdateWorkflow(temporal.UpdateCancellationRequested, "cancel-1", &testsuite.TestUpdateCallback{
			OnAccept:   func() {},
			OnReject:   func(err error) { t.Errorf("unexpected reject: %v", err) },
			OnComplete: func(any, error) {},
		}, temporal.CancellationRequest{RequestID: "cancel-1", RequestedBy: "user:customer-1"})
	}, 2*time.Hour)

	result := s.run(temporal.DefaultPolicy())

	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeCancellation, ReasonCode: temporal.ReasonCustomerRequested}, result)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityConfirmSlotReservation,
		temporal.ActivityPlanSchedule,
		temporal.ActivityRequestRestaurantOrder,
		temporal.ActivityCancelRestaurantOrder,
		temporal.ActivityReleaseSlotReservation,
		temporal.ActivityCancelPaymentAuthorization,
		temporal.ActivityFailOrCancelOrder,
	}, s.calls)
	assert.Equal(t, []string{
		string(temporal.StageWaitingAcceptance),
		string(temporal.StageScheduled),
		string(temporal.StageCompensating),
		string(temporal.StageCancelled),
	}, stages)
}

// FR-682: a history without the marker of the scheduled route — a run started
// before it — replays the ASAP route even with a window in its input.
func TestWorkflowWithoutScheduledRouteVersionTakesTheASAPRoute(t *testing.T) {
	s := newScheduled(t)
	s.env.OnGetVersion(temporal.ScheduledRouteChangeID, workflow.DefaultVersion, temporal.ScheduledRouteVersion).
		Return(workflow.DefaultVersion)
	s.deliverAll()

	result := s.run(policy())

	assert.Equal(t, temporal.OutcomeCompleted, result.Outcome)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityRequestRestaurantOrder,
		temporal.ActivityCreateDelivery,
		temporal.ActivityCapturePayment,
		temporal.ActivityCompleteOrder,
	}, s.calls)
}
