package fulfillorder_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"nomnom/orders/slices/order/infrastructure/temporal/fulfillorder"
)

const orderID = "order-1"

// harness runs the workflow with mocked Activities and records their calls.
type harness struct {
	t        *testing.T
	env      *testsuite.TestWorkflowEnvironment
	calls    []string
	steps    []fulfillorder.StepInput
	finalize fulfillorder.FinalizeInput
	declined map[string]bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	suite := &testsuite.WorkflowTestSuite{}
	h := &harness{t: t, env: suite.NewTestWorkflowEnvironment(), declined: map[string]bool{}}
	fulfillorder.Register(h.env, &fulfillorder.Activities{})

	record := func(name string) func(mock.Arguments) {
		return func(args mock.Arguments) {
			h.calls = append(h.calls, name)
			if step, ok := args.Get(1).(fulfillorder.StepInput); ok {
				h.steps = append(h.steps, step)
			}
			if finalize, ok := args.Get(1).(fulfillorder.FinalizeInput); ok {
				h.finalize = finalize
			}
		}
	}
	for _, name := range []string{fulfillorder.ActivityAuthorizePayment, fulfillorder.ActivityCapturePayment} {
		h.env.OnActivity(name, mock.Anything, mock.Anything).
			Return(func(context.Context, fulfillorder.StepInput) (fulfillorder.PaymentDecision, error) {
				if h.declined[name] {
					return fulfillorder.PaymentDecision{Reason: "card_declined"}, nil
				}
				return fulfillorder.PaymentDecision{Approved: true}, nil
			}).Run(record(name)).Maybe()
	}
	for _, name := range []string{
		fulfillorder.ActivityCancelPaymentAuthorization,
		fulfillorder.ActivityRefundPayment,
		fulfillorder.ActivityRequestRestaurantOrder,
		fulfillorder.ActivityCancelRestaurantOrder,
		fulfillorder.ActivityCreateDelivery,
		fulfillorder.ActivityReplanDelivery,
		fulfillorder.ActivityCancelDelivery,
		fulfillorder.ActivityCompleteOrder,
		fulfillorder.ActivityFailOrCancelOrder,
	} {
		h.env.OnActivity(name, mock.Anything, mock.Anything).Return(nil).Run(record(name)).Maybe()
	}
	return h
}

// declinePayment makes a financial Activity return a business decline.
func (h *harness) declinePayment(activity string) { h.declined[activity] = true }

func (h *harness) signalAt(after time.Duration, name, eventID string) {
	h.env.RegisterDelayedCallback(func() {
		h.env.SignalWorkflow(name, fulfillorder.Fact{EventID: eventID, OccurredAt: h.env.Now()})
	}, after)
}

func (h *harness) run(policy fulfillorder.Policy) fulfillorder.Result {
	h.t.Helper()
	h.env.ExecuteWorkflow(fulfillorder.WorkflowName, fulfillorder.Input{OrderID: orderID, Policy: policy})
	require.True(h.t, h.env.IsWorkflowCompleted())
	require.NoError(h.t, h.env.GetWorkflowError())
	var result fulfillorder.Result
	require.NoError(h.t, h.env.GetWorkflowResult(&result))
	return result
}

func policy() fulfillorder.Policy {
	p := fulfillorder.DefaultPolicy()
	p.PublishStage = false
	return p
}

// deliverAll schedules the positive route up to Delivery Completion.
func (h *harness) deliverAll() {
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, fulfillorder.SignalPreparationStarted, "started")
	h.signalAt(3*time.Minute, fulfillorder.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(4*time.Minute, fulfillorder.SignalRestaurantOrderReady, "ready")
	h.signalAt(5*time.Minute, fulfillorder.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(6*time.Minute, fulfillorder.SignalDeliveryCompleted, "delivered")
}

// AC-433: the route runs every Activity once in order and ends in Completion.
func TestWorkflowCompletesOrder(t *testing.T) {
	h := newHarness(t)
	h.deliverAll()

	p := fulfillorder.DefaultPolicy()
	result := h.run(p)

	assert.Equal(t, fulfillorder.Result{Outcome: fulfillorder.OutcomeCompleted}, result)
	assert.Equal(t, []string{
		fulfillorder.ActivityAuthorizePayment,
		fulfillorder.ActivityRequestRestaurantOrder,
		fulfillorder.ActivityCreateDelivery,
		fulfillorder.ActivityCapturePayment,
		fulfillorder.ActivityCompleteOrder,
	}, h.calls)
	assert.Equal(t, fulfillorder.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:authorize_payment"}, h.steps[0])
	assert.Equal(t, fulfillorder.StepInput{OrderID: orderID, IdempotencyKey: "order:order-1:capture_payment"}, h.steps[3])
}

// AC-434: a declined Authorization ends the Order without compensations.
func TestWorkflowFailsOnDeclinedAuthorization(t *testing.T) {
	h := newHarness(t)
	h.declinePayment(fulfillorder.ActivityAuthorizePayment)

	result := h.run(policy())

	assert.Equal(t, fulfillorder.Result{Outcome: fulfillorder.OutcomeFailure, ReasonCode: fulfillorder.ReasonPaymentDeclined}, result)
	assert.Equal(t, []string{fulfillorder.ActivityAuthorizePayment, fulfillorder.ActivityFailOrCancelOrder}, h.calls)
	assert.Equal(t, fulfillorder.OutcomeFailure, h.finalize.Outcome)
	assert.Equal(t, fulfillorder.ReasonPaymentDeclined, h.finalize.ReasonCode)
}

// AC-435: no Acceptance before the deadline cancels the Restaurant Order and Authorization.
func TestWorkflowCompensatesAcceptanceTimeout(t *testing.T) {
	h := newHarness(t)

	result := h.run(policy())

	assert.Equal(t, fulfillorder.ReasonRestaurantAcceptanceTimeout, result.ReasonCode)
	assert.Equal(t, []string{
		fulfillorder.ActivityAuthorizePayment,
		fulfillorder.ActivityRequestRestaurantOrder,
		fulfillorder.ActivityCancelRestaurantOrder,
		fulfillorder.ActivityCancelPaymentAuthorization,
		fulfillorder.ActivityFailOrCancelOrder,
	}, h.calls)
}

// AC-436: PreparationDelayed moves the Preparation deadline.
func TestWorkflowExtendsPreparationOnDelay(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, fulfillorder.SignalCourierAssignedToDelivery, "assigned")
	h.env.RegisterDelayedCallback(func() {
		h.env.SignalWorkflow(fulfillorder.SignalPreparationDelayed, fulfillorder.Fact{
			EventID:         "delayed",
			ExpectedReadyAt: h.env.Now().Add(40 * time.Minute),
		})
	}, 80*time.Minute)
	h.signalAt(110*time.Minute, fulfillorder.SignalRestaurantOrderReady, "ready")
	h.signalAt(111*time.Minute, fulfillorder.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(112*time.Minute, fulfillorder.SignalDeliveryCompleted, "delivered")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.OutcomeCompleted, result.Outcome)
}

func TestWorkflowFailsOnPreparationTimeout(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, fulfillorder.SignalCourierAssignedToDelivery, "assigned")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.ReasonPreparationTimeout, result.ReasonCode)
}

// AC-437: exhausted replans escalate to the operator, who cancels.
func TestWorkflowEscalatesAssignmentToOperator(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(10*time.Minute, fulfillorder.SignalRestaurantOrderReady, "ready")
	h.env.RegisterDelayedCallback(func() {
		h.env.SignalWorkflow(fulfillorder.SignalOperatorDecision, fulfillorder.OperatorDecision{
			DecisionID: "decision-1",
			Choice:     fulfillorder.OperatorCancel,
		})
	}, 50*time.Minute)

	result := h.run(policy())

	assert.Equal(t, fulfillorder.Result{Outcome: fulfillorder.OutcomeCancellation, ReasonCode: fulfillorder.ReasonOperatorDecision}, result)
	assert.Equal(t, []string{
		fulfillorder.ActivityAuthorizePayment,
		fulfillorder.ActivityRequestRestaurantOrder,
		fulfillorder.ActivityCreateDelivery,
		fulfillorder.ActivityReplanDelivery,
		fulfillorder.ActivityReplanDelivery,
		fulfillorder.ActivityCancelDelivery,
		fulfillorder.ActivityCancelRestaurantOrder,
		fulfillorder.ActivityCancelPaymentAuthorization,
		fulfillorder.ActivityFailOrCancelOrder,
	}, h.calls)
	assert.Equal(t, "order:order-1:replan_delivery_1", h.steps[3].IdempotencyKey)
	assert.Equal(t, "order:order-1:replan_delivery_2", h.steps[4].IdempotencyKey)
}

func TestWorkflowFailsWithoutOperatorDecision(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(10*time.Minute, fulfillorder.SignalRestaurantOrderReady, "ready")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.ReasonCourierAssignmentExhausted, result.ReasonCode)
}

func TestWorkflowRetriesAssignmentOnOperatorDecision(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(10*time.Minute, fulfillorder.SignalRestaurantOrderReady, "ready")
	h.env.RegisterDelayedCallback(func() {
		h.env.SignalWorkflow(fulfillorder.SignalOperatorDecision, fulfillorder.OperatorDecision{
			DecisionID: "decision-1",
			Choice:     fulfillorder.OperatorRetryAssignment,
		})
	}, 50*time.Minute)
	h.signalAt(55*time.Minute, fulfillorder.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(56*time.Minute, fulfillorder.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(57*time.Minute, fulfillorder.SignalDeliveryCompleted, "delivered")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.OutcomeCompleted, result.Outcome)
	assert.Equal(t, "order:order-1:replan_delivery_3", h.steps[5].IdempotencyKey)
}

// AC-438: a rejection after Acceptance runs the Saga without cancelling the Restaurant Order.
func TestWorkflowCompensatesRejectionAfterAcceptance(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, fulfillorder.SignalRestaurantOrderRejected, "rejected")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.ReasonRestaurantRejected, result.ReasonCode)
	assert.Equal(t, []string{
		fulfillorder.ActivityAuthorizePayment,
		fulfillorder.ActivityRequestRestaurantOrder,
		fulfillorder.ActivityCreateDelivery,
		fulfillorder.ActivityCancelDelivery,
		fulfillorder.ActivityCancelPaymentAuthorization,
		fulfillorder.ActivityFailOrCancelOrder,
	}, h.calls)
}

// AC-439: a Delivery failure after Capture refunds the payment.
func TestWorkflowRefundsAfterCapture(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, fulfillorder.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(3*time.Minute, fulfillorder.SignalRestaurantOrderReady, "ready")
	h.signalAt(4*time.Minute, fulfillorder.SignalDeliveryPickedUp, "picked-up")
	h.signalAt(5*time.Minute, fulfillorder.SignalDeliveryFailed, "failed")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.ReasonDeliveryFailed, result.ReasonCode)
	assert.Equal(t, []string{
		fulfillorder.ActivityAuthorizePayment,
		fulfillorder.ActivityRequestRestaurantOrder,
		fulfillorder.ActivityCreateDelivery,
		fulfillorder.ActivityCapturePayment,
		fulfillorder.ActivityRefundPayment,
		fulfillorder.ActivityFailOrCancelOrder,
	}, h.calls)
}

func TestWorkflowFailsOnDeclinedCapture(t *testing.T) {
	h := newHarness(t)
	h.declinePayment(fulfillorder.ActivityCapturePayment)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, fulfillorder.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(3*time.Minute, fulfillorder.SignalRestaurantOrderReady, "ready")
	h.signalAt(4*time.Minute, fulfillorder.SignalDeliveryPickedUp, "picked-up")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.ReasonPaymentCaptureDeclined, result.ReasonCode)
	assert.Equal(t, []string{
		fulfillorder.ActivityAuthorizePayment,
		fulfillorder.ActivityRequestRestaurantOrder,
		fulfillorder.ActivityCreateDelivery,
		fulfillorder.ActivityCapturePayment,
		fulfillorder.ActivityCancelDelivery,
		fulfillorder.ActivityCancelPaymentAuthorization,
		fulfillorder.ActivityFailOrCancelOrder,
	}, h.calls)
}

// AC-440: a repeated event_id does not repeat a step.
func TestWorkflowDeduplicatesEvents(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(3*time.Minute, fulfillorder.SignalPreparationFailed, "failed")
	h.signalAt(4*time.Minute, fulfillorder.SignalPreparationFailed, "failed")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.ReasonPreparationFailed, result.ReasonCode)
	assert.Equal(t, 1, count(h.calls, fulfillorder.ActivityCreateDelivery))
	assert.Equal(t, 1, count(h.calls, fulfillorder.ActivityFailOrCancelOrder))
	assert.Zero(t, count(h.calls, fulfillorder.ActivityCancelRestaurantOrder))
}

// AC-441: readiness before Acceptance is ignored, so the Preparation deadline still fires.
func TestWorkflowIgnoresReadyBeforeAcceptance(t *testing.T) {
	h := newHarness(t)
	h.signalAt(30*time.Second, fulfillorder.SignalRestaurantOrderReady, "early-ready")
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, fulfillorder.SignalCourierAssignedToDelivery, "assigned")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.ReasonPreparationTimeout, result.ReasonCode)
}

// AC-442: a Cancellation before Acceptance cancels the Restaurant Order and Authorization.
func TestWorkflowCancelsOnUpdate(t *testing.T) {
	h := newHarness(t)
	var accepted, repeated bool
	request := fulfillorder.CancellationRequest{RequestID: "cancel-1", RequestedBy: "user:customer-1"}
	h.env.RegisterDelayedCallback(func() {
		h.env.UpdateWorkflow(fulfillorder.UpdateCancellationRequested, request.RequestID, &testsuite.TestUpdateCallback{
			OnAccept:   func() { accepted = true },
			OnReject:   func(err error) { t.Errorf("unexpected reject: %v", err) },
			OnComplete: func(any, error) {},
		}, request)
		h.env.UpdateWorkflow(fulfillorder.UpdateCancellationRequested, request.RequestID+"-retry", &testsuite.TestUpdateCallback{
			OnAccept:   func() { repeated = true },
			OnReject:   func(error) {},
			OnComplete: func(any, error) {},
		}, request)
	}, time.Minute)

	result := h.run(policy())

	assert.True(t, accepted)
	assert.True(t, repeated, "a repeated RequestID is admitted")
	assert.Equal(t, fulfillorder.Result{Outcome: fulfillorder.OutcomeCancellation, ReasonCode: fulfillorder.ReasonCustomerRequested}, result)
	assert.Equal(t, []string{
		fulfillorder.ActivityAuthorizePayment,
		fulfillorder.ActivityRequestRestaurantOrder,
		fulfillorder.ActivityCancelRestaurantOrder,
		fulfillorder.ActivityCancelPaymentAuthorization,
		fulfillorder.ActivityFailOrCancelOrder,
	}, h.calls)
	assert.Equal(t, fulfillorder.OutcomeCancellation, h.finalize.Outcome)
}

// AC-443: after Pickup the Update is refused and the route continues.
func TestWorkflowRefusesCancellationAfterPickup(t *testing.T) {
	h := newHarness(t)
	h.signalAt(time.Minute, fulfillorder.SignalRestaurantOrderAccepted, "accepted")
	h.signalAt(2*time.Minute, fulfillorder.SignalCourierAssignedToDelivery, "assigned")
	h.signalAt(3*time.Minute, fulfillorder.SignalRestaurantOrderReady, "ready")
	h.signalAt(4*time.Minute, fulfillorder.SignalDeliveryPickedUp, "picked-up")
	var rejection error
	h.env.RegisterDelayedCallback(func() {
		h.env.UpdateWorkflow(fulfillorder.UpdateCancellationRequested, "cancel-1", &testsuite.TestUpdateCallback{
			OnAccept:   func() { t.Error("unexpected accept") },
			OnReject:   func(err error) { rejection = err },
			OnComplete: func(any, error) {},
		}, fulfillorder.CancellationRequest{RequestID: "cancel-1"})
	}, 5*time.Minute)
	h.signalAt(6*time.Minute, fulfillorder.SignalDeliveryCompleted, "delivered")

	result := h.run(policy())

	assert.Equal(t, fulfillorder.OutcomeCompleted, result.Outcome)
	var application *temporal.ApplicationError
	require.True(t, errors.As(rejection, &application))
	assert.Equal(t, fulfillorder.ErrTypeCancellationRefused, application.Type())
}

func TestWorkflowRejectsEmptyOrderID(t *testing.T) {
	h := newHarness(t)
	h.env.ExecuteWorkflow(fulfillorder.WorkflowName, fulfillorder.Input{Policy: policy()})

	var application *temporal.ApplicationError
	require.True(t, errors.As(h.env.GetWorkflowError(), &application))
	assert.Equal(t, fulfillorder.ErrTypeInvalidInput, application.Type())
}

func count(calls []string, name string) int {
	n := 0
	for _, call := range calls {
		if call == name {
			n++
		}
	}
	return n
}
