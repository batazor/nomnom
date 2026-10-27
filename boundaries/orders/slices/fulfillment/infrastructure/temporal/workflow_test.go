package temporal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

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
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	suite := &testsuite.WorkflowTestSuite{}
	h := &harness{t: t, env: suite.NewTestWorkflowEnvironment(), declined: map[string]bool{}}
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
		temporal.ActivityRequestRestaurantOrder,
		temporal.ActivityCancelRestaurantOrder,
		temporal.ActivityCreateDelivery,
		temporal.ActivityReplanDelivery,
		temporal.ActivityCancelDelivery,
		temporal.ActivityCompleteOrder,
		temporal.ActivityFailOrCancelOrder,
	} {
		h.env.OnActivity(name, mock.Anything, mock.Anything).Return(nil).Run(record(name)).Maybe()
	}
	return h
}

// declinePayment makes a financial Activity return a business decline.
func (h *harness) declinePayment(activity string) { h.declined[activity] = true }

func (h *harness) signalAt(after time.Duration, name, eventID string) {
	h.env.RegisterDelayedCallback(func() {
		h.env.SignalWorkflow(name, temporal.Fact{EventID: eventID, OccurredAt: h.env.Now()})
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
		temporal.ActivityCapturePayment,
		temporal.ActivityCompleteOrder,
	}, h.calls)
	assert.Equal(t, []temporal.StepInput{
		{OrderID: orderID, IdempotencyKey: "order:order-1:authorize_payment"},
		{OrderID: orderID, IdempotencyKey: "order:order-1:record_payment_authorization", DecidedAt: h.steps[1].DecidedAt},
		{OrderID: orderID, IdempotencyKey: "order:order-1:request_restaurant_order"},
		{OrderID: orderID, IdempotencyKey: "order:order-1:create_delivery"},
		{OrderID: orderID, IdempotencyKey: "order:order-1:capture_payment"},
		{OrderID: orderID, IdempotencyKey: "order:order-1:complete_order", DecidedAt: h.steps[len(h.steps)-1].DecidedAt},
	}, h.steps)
	// The payment and completion moments are the workflow time of their steps.
	assert.False(t, h.steps[1].DecidedAt.IsZero())
	assert.False(t, h.steps[len(h.steps)-1].DecidedAt.IsZero())
	for _, step := range append([]temporal.StepInput{h.steps[0]}, h.steps[2:len(h.steps)-1]...) {
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
		h.env.SignalWorkflow(temporal.SignalPreparationDelayed, temporal.Fact{
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

// AC-437: deadline timers replan and escalate, the OperatorDecision Signal
// reaches the model and the Saga runs as Activities.
func TestWorkflowEscalatesAssignmentToOperator(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, temporal.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(10*time.Minute, temporal.SignalRestaurantOrderReady, "ready")
	h.env.RegisterDelayedCallback(func() {
		h.env.SignalWorkflow(temporal.SignalOperatorDecision, temporal.OperatorDecision{
			DecisionID: "decision-1",
			Choice:     temporal.OperatorCancel,
		})
	}, 50*time.Minute)

	result := h.run(policy())

	assert.Equal(t, temporal.Result{Outcome: temporal.OutcomeCancellation, ReasonCode: temporal.ReasonOperatorDecision}, result)
	assert.Equal(t, []string{
		temporal.ActivityAuthorizePayment,
		temporal.ActivityRecordPaymentAuthorization,
		temporal.ActivityRequestRestaurantOrder,
		temporal.ActivityCreateDelivery,
		temporal.ActivityReplanDelivery,
		temporal.ActivityReplanDelivery,
		temporal.ActivityCancelDelivery,
		temporal.ActivityCancelRestaurantOrder,
		temporal.ActivityCancelPaymentAuthorization,
		temporal.ActivityFailOrCancelOrder,
	}, h.calls)
	assert.Equal(t, "order:order-1:replan_delivery_1", h.steps[4].IdempotencyKey)
	assert.Equal(t, "order:order-1:replan_delivery_2", h.steps[5].IdempotencyKey)
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
		temporal.ActivityCapturePayment,
		temporal.ActivityRefundPayment,
		temporal.ActivityFailOrCancelOrder,
	}, h.calls)
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
