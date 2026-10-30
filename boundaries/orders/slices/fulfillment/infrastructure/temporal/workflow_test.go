package temporal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"nomnom/orders/slices/fulfillment/infrastructure/temporal"
)

const orderID = "order-1"

// harness runs the workflow with mocked Activities and records their calls.
type harness struct {
	t        *testing.T
	env      *testsuite.TestWorkflowEnvironment
	calls    []string
	steps    []temporal.StepInput
	finalize temporal.FinalizeInput
	declined map[string]bool
	// priorities are the priority keys the Activities were scheduled with.
	priorities map[string]int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	suite := &testsuite.WorkflowTestSuite{}
	h := &harness{t: t, env: suite.NewTestWorkflowEnvironment(), declined: map[string]bool{}, priorities: map[string]int{}}
	h.env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{&priorityRecorder{priorities: h.priorities}}})
	temporal.Register(h.env, &temporal.Activities{})

	record := func(name string) func(mock.Arguments) {
		return func(args mock.Arguments) {
			h.calls = append(h.calls, name)
			if step, ok := args.Get(1).(temporal.StepInput); ok {
				h.steps = append(h.steps, step)
			}
			if finalize, ok := args.Get(1).(temporal.FinalizeInput); ok {
				h.finalize = finalize
			}
		}
	}
	for _, name := range []string{temporal.ActivityAuthorizePayment, temporal.ActivityCapturePayment} {
		h.env.OnActivity(name, mock.Anything, mock.Anything).
			Return(func(context.Context, temporal.StepInput) (temporal.PaymentDecision, error) {
				if h.declined[name] {
					return temporal.PaymentDecision{Reason: "card_declined"}, nil
				}
				return temporal.PaymentDecision{Approved: true}, nil
			}).Run(record(name)).Maybe()
	}
	for _, name := range []string{
		temporal.ActivityCancelPaymentAuthorization,
		temporal.ActivityRefundPayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityCancelRestaurantOrder,
		temporal.ActivityRecordRestaurantPickup,
		temporal.ActivityCreateDelivery,
		temporal.ActivityCancelDelivery,
		temporal.ActivityCompleteOrder,
		temporal.ActivityFailOrCancelOrder,
	} {
		h.env.OnActivity(name, mock.Anything, mock.Anything).Return(nil).Run(record(name)).Maybe()
	}
	h.env.OnActivity(temporal.ActivityRequestRestaurantOrder, mock.Anything, mock.Anything).
		Return(temporal.RestaurantOrderRequested{RestaurantOrderID: "ro-1"}, nil).
		Run(record(temporal.ActivityRequestRestaurantOrder)).Maybe()
	return h
}

// priorityRecorder records the priority key of every scheduled Activity: the
// test environment does not pass it to the Activity Info.
type priorityRecorder struct {
	interceptor.WorkerInterceptorBase
	priorities map[string]int
}

func (r *priorityRecorder) InterceptWorkflow(_ workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	return &priorityInbound{WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next}, priorities: r.priorities}
}

type priorityInbound struct {
	interceptor.WorkflowInboundInterceptorBase
	priorities map[string]int
}

func (i *priorityInbound) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	return i.Next.Init(&priorityOutbound{WorkflowOutboundInterceptorBase: interceptor.WorkflowOutboundInterceptorBase{Next: outbound}, priorities: i.priorities})
}

type priorityOutbound struct {
	interceptor.WorkflowOutboundInterceptorBase
	priorities map[string]int
}

func (o *priorityOutbound) ExecuteActivity(ctx workflow.Context, activityType string, args ...any) workflow.Future {
	o.priorities[activityType] = workflow.GetActivityOptions(ctx).Priority.PriorityKey
	return o.Next.ExecuteActivity(ctx, activityType, args...)
}

// declinePayment makes a financial Activity return a business decline.
func (h *harness) declinePayment(activity string) { h.declined[activity] = true }

func (h *harness) signalAt(after time.Duration, name, eventID string) {
	h.env.RegisterDelayedCallback(func() {
		h.env.SignalWorkflow(name, temporal.SignalPayload{EventID: eventID, OccurredAt: h.env.Now()})
	}, after)
}

func (h *harness) run(policy temporal.Policy) temporal.Result {
	h.t.Helper()
	h.env.ExecuteWorkflow(temporal.WorkflowName, temporal.Input{OrderID: orderID, Policy: policy})
	require.True(h.t, h.env.IsWorkflowCompleted())
	require.NoError(h.t, h.env.GetWorkflowError())
	var result temporal.Result
	require.NoError(h.t, h.env.GetWorkflowResult(&result))
	return result
}

func policy() temporal.Policy {
	p := temporal.DefaultPolicy()
	p.PublishStage = false
	return p
}

// deliverAll schedules the positive route up to Delivery Completion.
func (h *harness) deliverAll() {
	h.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, temporal.SignalPreparationStarted, "started")
	h.signalAt(3*time.Minute, temporal.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(4*time.Minute, temporal.SignalRestaurantOrderReady, "ready")
	h.signalAt(5*time.Minute, temporal.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(6*time.Minute, temporal.SignalDeliveryCompleted, "delivered")
}

// AC-433, AC-548: Signals reach the model, its steps run as the same
// Activities with the same idempotency keys, and the route ends in Completion.
func TestWorkflowCompletesOrder(t *testing.T) {
	h := newHarness(t)
	h.deliverAll()

	p := temporal.DefaultPolicy()
	result := h.run(p)

	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeCompleted}, result)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityRequestRestaurantOrder,
		temporal.ActivityCreateDelivery,
		temporal.ActivityRecordRestaurantPickup,
		temporal.ActivityCapturePayment,
		temporal.ActivityCompleteOrder,
	}, h.calls)
	assert.Equal(t, []temporal.StepInput{
		{OrderID: orderID, IdempotencyKey: "order:order-1:authorize_payment"},
		{OrderID: orderID, IdempotencyKey: "order:order-1:record_payment_authorization", DecidedAt: h.steps[1].DecidedAt},
		{OrderID: orderID, IdempotencyKey: "order:order-1:request_restaurant_order"},
		{OrderID: orderID, IdempotencyKey: "order:order-1:create_delivery"},
		{OrderID: orderID, IdempotencyKey: "order:order-1:record_restaurant_pickup", DecidedAt: h.steps[4].DecidedAt},
		{OrderID: orderID, IdempotencyKey: "order:order-1:capture_payment"},
		{OrderID: orderID, IdempotencyKey: "order:order-1:complete_order", DecidedAt: h.steps[len(h.steps)-1].DecidedAt},
	}, h.steps)
	// The payment and completion moments are the workflow time of their steps,
	// the kitchen gets the moment of the Pickup.
	assert.False(t, h.steps[1].DecidedAt.IsZero())
	assert.False(t, h.steps[4].DecidedAt.IsZero())
	assert.False(t, h.steps[len(h.steps)-1].DecidedAt.IsZero())
	for _, step := range []temporal.StepInput{h.steps[0], h.steps[2], h.steps[3], h.steps[5]} {
		assert.True(t, step.DecidedAt.IsZero(), step.IdempotencyKey)
	}
}

// The Workflow Stage is published as OrderFulfillmentStage once per change.
func TestWorkflowPublishesStage(t *testing.T) {
	h := newHarness(t)
	h.deliverAll()
	var stages []string
	h.env.OnUpsertTypedSearchAttributes(mock.Anything).Run(func(args mock.Arguments) {
		stage, ok := args.Get(0).(sdktemporal.SearchAttributes).GetKeyword(temporal.StageSearchAttribute)
		require.True(t, ok)
		stages = append(stages, stage)
	}).Return(nil)

	h.run(temporal.DefaultPolicy())

	assert.Equal(t, []string{
		string(temporal.StageWaitingAcceptance),
		string(temporal.StagePreparing),
		string(temporal.StageWaitingPickup),
		string(temporal.StageDelivering),
		string(temporal.StageCompleting),
		string(temporal.StageCompleted),
	}, stages)
}

// AC-434: a declined Authorization ends the Order without compensations.
func TestWorkflowFailsOnDeclinedAuthorization(t *testing.T) {
	h := newHarness(t)
	h.declinePayment(temporal.ActivityAuthorizePayment)

	result := h.run(policy())

	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeFailure, ReasonCode: temporal.ReasonPaymentDeclined}, result)
	assert.Equal(t, []string{temporal.ActivityAuthorizePayment, temporal.ActivityFailOrCancelOrder}, h.calls)
	assert.Equal(t, temporal.OutcomeFailure, h.finalize.Outcome)
	assert.Equal(t, temporal.ReasonPaymentDeclined, h.finalize.ReasonCode)
}

// AC-436: ExpectedReadyAt of PreparationDelayed reaches the model and moves the timer.
func TestWorkflowExtendsPreparationOnDelay(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, temporal.SignalCourierAssignedToDelivery, "assigned")
	h.env.RegisterDelayedCallback(func() {
		h.env.SignalWorkflow(temporal.SignalPreparationDelayed, temporal.SignalPayload{
			EventID:         "delayed",
			ExpectedReadyAt: h.env.Now().Add(40 * time.Minute),
		})
	}, 80*time.Minute)
	h.signalAt(110*time.Minute, temporal.SignalRestaurantOrderReady, "ready")
	h.signalAt(111*time.Minute, temporal.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(112*time.Minute, temporal.SignalDeliveryCompleted, "delivered")

	result := h.run(policy())

	assert.Equal(t, temporal.OutcomeCompleted, result.Outcome)
}

// AC-968: the workflow waits for Courier Assignment without a deadline: a
// day without a courier calls no Activity of delivery and changes no Order.
func TestWorkflowWaitsForCourierWithoutDeadline(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(10*time.Minute, temporal.SignalRestaurantOrderReady, "ready")
	h.signalAt(24*time.Hour, temporal.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(24*time.Hour+time.Minute, temporal.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(24*time.Hour+2*time.Minute, temporal.SignalDeliveryCompleted, "delivered")

	result := h.run(policy())

	assert.Equal(t, temporal.OutcomeCompleted, result.Outcome)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityRequestRestaurantOrder,
		temporal.ActivityCreateDelivery,
		temporal.ActivityRecordRestaurantPickup,
		temporal.ActivityCapturePayment,
		temporal.ActivityCompleteOrder,
	}, h.calls)
}

// AC-439: a Delivery failure Signal after Capture runs the refund Saga.
func TestWorkflowRefundsAfterCapture(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, temporal.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(3*time.Minute, temporal.SignalRestaurantOrderReady, "ready")
	h.signalAt(4*time.Minute, temporal.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(5*time.Minute, temporal.SignalDeliveryFailed, "failed")

	result := h.run(policy())

	assert.Equal(t, temporal.ReasonDeliveryFailed, result.ReasonCode)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityRequestRestaurantOrder,
		temporal.ActivityCreateDelivery,
		temporal.ActivityRecordRestaurantPickup,
		temporal.ActivityCapturePayment,
		temporal.ActivityRefundPayment,
		temporal.ActivityFailOrCancelOrder,
	}, h.calls)
}

// Task Queue Priority: the customer-facing payment steps go first, the kitchen,
// the courier and compensations next, bookkeeping last.
func TestWorkflowSchedulesActivitiesWithTheirPriority(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, temporal.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(3*time.Minute, temporal.SignalRestaurantOrderReady, "ready")
	h.signalAt(4*time.Minute, temporal.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(5*time.Minute, temporal.SignalDeliveryFailed, "failed")

	h.run(policy())

	assert.Equal(t, map[string]int{
		temporal.ActivityAuthorizePayment:           temporal.PriorityCustomer,
		temporal.ActivityRefundPayment:              temporal.PriorityCustomer,
		temporal.ActivityRecordPaymentAuthorization: temporal.PriorityFulfillment,
		temporal.ActivityRequestRestaurantOrder:     temporal.PriorityFulfillment,
		temporal.ActivityCreateDelivery:             temporal.PriorityFulfillment,
		temporal.ActivityRecordRestaurantPickup:     temporal.PriorityFulfillment,
		temporal.ActivityCapturePayment:             temporal.PriorityFulfillment,
		temporal.ActivityFailOrCancelOrder:          temporal.PriorityFulfillment,
	}, h.priorities)
}

// AC-442: a Cancellation before Acceptance cancels the Restaurant Order and Authorization.
func TestWorkflowCancelsOnUpdate(t *testing.T) {
	h := newHarness(t)
	var accepted, repeated bool
	request := temporal.CancellationRequest{RequestID: "cancel-1", RequestedBy: "user:customer-1"}
	h.env.RegisterDelayedCallback(func() {
		h.env.UpdateWorkflow(temporal.UpdateCancellationRequested, request.RequestID, &testsuite.TestUpdateCallback{
			OnAccept:   func() { accepted = true },
			OnReject:   func(err error) { t.Errorf("unexpected reject: %v", err) },
			OnComplete: func(any, error) {},
		}, request)
		h.env.UpdateWorkflow(temporal.UpdateCancellationRequested, request.RequestID+"-retry", &testsuite.TestUpdateCallback{
			OnAccept:   func() { repeated = true },
			OnReject:   func(error) {},
			OnComplete: func(any, error) {},
		}, request)
	}, time.Minute)

	result := h.run(policy())

	assert.True(t, accepted)
	assert.True(t, repeated, "a repeated RequestID is admitted")
	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeCancellation, ReasonCode: temporal.ReasonCustomerRequested}, result)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityRequestRestaurantOrder,
		temporal.ActivityCancelRestaurantOrder,
		temporal.ActivityCancelPaymentAuthorization,
		temporal.ActivityFailOrCancelOrder,
	}, h.calls)
	assert.Equal(t, temporal.OutcomeCancellation, h.finalize.Outcome)
}

// AC-443: after Pickup the Update is refused and the route continues.
func TestWorkflowRefusesCancellationAfterPickup(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, temporal.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(3*time.Minute, temporal.SignalRestaurantOrderReady, "ready")
	h.signalAt(4*time.Minute, temporal.SignalDeliveryPickedUp, "picked-up")
	var rejection error
	h.env.RegisterDelayedCallback(func() {
		h.env.UpdateWorkflow(temporal.UpdateCancellationRequested, "cancel-1", &testsuite.TestUpdateCallback{
			OnAccept:   func() { t.Error("unexpected accept") },
			OnReject:   func(err error) { rejection = err },
			OnComplete: func(any, error) {},
		}, temporal.CancellationRequest{RequestID: "cancel-1"})
	}, 5*time.Minute)
	h.signalAt(6*time.Minute, temporal.SignalDeliveryCompleted, "delivered")

	result := h.run(policy())

	assert.Equal(t, temporal.OutcomeCompleted, result.Outcome)
	var application *sdktemporal.ApplicationError
	require.True(t, errors.As(rejection, &application))
	assert.Equal(t, temporal.ErrTypeCancellationRefused, application.Type())
}

func TestWorkflowRejectsEmptyOrderID(t *testing.T) {
	h := newHarness(t)
	h.env.ExecuteWorkflow(temporal.WorkflowName, temporal.Input{Policy: policy()})

	var application *sdktemporal.ApplicationError
	require.True(t, errors.As(h.env.GetWorkflowError(), &application))
	assert.Equal(t, temporal.ErrTypeInvalidInput, application.Type())
}

// A run recorded before the Pickup step replays without it.
func TestWorkflowWithoutRestaurantPickupVersionSkipsTheStep(t *testing.T) {
	h := newHarness(t)
	h.env.OnGetVersion(temporal.RestaurantPickupChangeID, workflow.DefaultVersion, temporal.RestaurantPickupVersion).
		Return(workflow.DefaultVersion)
	h.deliverAll()

	result := h.run(policy())

	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeCompleted}, result)
	assert.NotContains(t, h.calls, temporal.ActivityRecordRestaurantPickup)
}

// The Pickup reaches the kitchen with its own moment, once the kitchen reported
// the order ready: an earlier one waits.
func TestWorkflowReportsPickupToTheKitchenAfterReady(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, temporal.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(3*time.Minute, temporal.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(4*time.Minute, temporal.SignalRestaurantOrderReady, "ready")
	h.signalAt(5*time.Minute, temporal.SignalDeliveryCompleted, "delivered")

	result := h.run(policy())

	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeCompleted}, result)
	var capture, pickup int
	for i, call := range h.calls {
		switch call {
		case temporal.ActivityCapturePayment:
			capture = i
		case temporal.ActivityRecordRestaurantPickup:
			pickup = i
		}
	}
	assert.Less(t, capture, pickup, "the money does not wait for the kitchen")
	var reported temporal.StepInput
	for _, step := range h.steps {
		if step.IdempotencyKey == temporal.WorkflowID(orderID)+":record_restaurant_pickup" {
			reported = step
		}
	}
	assert.False(t, reported.DecidedAt.IsZero(), "the kitchen gets the moment of the Pickup")
}
