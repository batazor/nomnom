// Package fulfillorder is the Temporal adapter of the Order Fulfillment Workflow:
// the workflow, its Activities and the client that implements the application
// port fulfillment. The workflow code does not know the Order model, Commands
// or handlers; Activities and the client translate to them.
package fulfillorder

import "time"

const (
	WorkflowName = "OrderFulfillment"
	TaskQueue    = "orders"

	workflowIDPrefix = "order:"
)

// WorkflowID is the only identity of the workflow of one Order.
func WorkflowID(orderID string) string { return workflowIDPrefix + orderID }

// Signal names equal the names of the Integration Events that cause them.
const (
	SignalOrderPlaced               = "OrderPlaced"
	SignalPaymentAuthorized         = "PaymentAuthorized"
	SignalPaymentFailed             = "PaymentFailed"
	SignalRestaurantOrderAccepted   = "RestaurantOrderAccepted"
	SignalRestaurantOrderRejected   = "RestaurantOrderRejected"
	SignalPreparationStarted        = "PreparationStarted"
	SignalPreparationDelayed        = "PreparationDelayed"
	SignalPreparationFailed         = "PreparationFailed"
	SignalRestaurantOrderReady      = "RestaurantOrderReady"
	SignalCourierAssignedToDelivery = "CourierAssignedToDelivery"
	SignalDeliveryPickedUp          = "DeliveryPickedUp"
	SignalDeliveryCompleted         = "DeliveryCompleted"
	SignalDeliveryFailed            = "DeliveryFailed"
	SignalOperatorDecision          = "OperatorDecision"

	UpdateCancellationRequested = "CancellationRequested"
)

// Activity names registered in the worker.
const (
	ActivityAuthorizePayment           = "AuthorizePayment"
	ActivityCapturePayment             = "CapturePayment"
	ActivityCancelPaymentAuthorization = "CancelPaymentAuthorization"
	ActivityRefundPayment              = "RefundPayment"
	ActivityRequestRestaurantOrder     = "RequestRestaurantOrder"
	ActivityCancelRestaurantOrder      = "CancelRestaurantOrder"
	ActivityCreateDelivery             = "CreateDelivery"
	ActivityReplanDelivery             = "ReplanDelivery"
	ActivityCancelDelivery             = "CancelDelivery"
	ActivityCompleteOrder              = "CompleteOrder"
	ActivityFailOrCancelOrder          = "FailOrCancelOrder"
)

// Application error types crossing the workflow boundary.
const (
	ErrTypeCancellationRefused = "CancellationRefused"
	ErrTypeInvalidInput        = "InvalidInput"
	ErrTypeOrderNotFound       = "OrderNotFound"
	ErrTypeInvalidTransition   = "InvalidTransition"
)

// Input starts the workflow. Policy is fixed in Event History, so a later
// configuration change does not affect running workflows.
type Input struct {
	OrderID string
	Policy  Policy
}

// Fact is the payload of every Integration Event Signal.
type Fact struct {
	EventID    string
	OccurredAt time.Time
	Reason     string
	// ExpectedReadyAt is set only for PreparationDelayed.
	ExpectedReadyAt time.Time
}

type OperatorChoice string

const (
	OperatorRetryAssignment OperatorChoice = "retry_assignment"
	OperatorCancel          OperatorChoice = "cancel"
)

// OperatorDecision is the payload of the OperatorDecision Signal.
type OperatorDecision struct {
	DecisionID string
	Choice     OperatorChoice
}

// CancellationRequest is the argument of the CancellationRequested Update.
type CancellationRequest struct {
	RequestID   string
	RequestedBy string
	RequestedAt time.Time
	Reason      string
}

type Outcome string

const (
	OutcomeCompleted    Outcome = "completed"
	OutcomeCancellation Outcome = "cancellation"
	OutcomeFailure      Outcome = "failure"
)

// Reason codes of the negative outcome passed to FailOrCancelOrder.
const (
	ReasonPaymentDeclined             = "payment_declined"
	ReasonPaymentCaptureDeclined      = "payment_capture_declined"
	ReasonRestaurantRejected          = "restaurant_rejected"
	ReasonRestaurantAcceptanceTimeout = "restaurant_acceptance_timeout"
	ReasonPreparationFailed           = "preparation_failed"
	ReasonPreparationTimeout          = "preparation_timeout"
	ReasonCourierAssignmentExhausted  = "courier_assignment_exhausted"
	ReasonPickupTimeout               = "pickup_timeout"
	ReasonDeliveryFailed              = "delivery_failed"
	ReasonDeliveryTimeout             = "delivery_timeout"
	ReasonCustomerRequested           = "customer_requested"
	ReasonOperatorDecision            = "operator_decision"
)

// Result is the final business outcome of the workflow.
type Result struct {
	Outcome    Outcome
	ReasonCode string
}

// StepInput addresses one logical step of the Order; IdempotencyKey is stable
// across Activity attempts and workflow runs.
type StepInput struct {
	OrderID        string
	IdempotencyKey string
}

// PaymentDecision is the business result of a financial operation; a decline
// is a result, not an error.
type PaymentDecision struct {
	Approved bool
	Reason   string
}

// FinalizeInput carries the negative outcome chosen by the workflow.
type FinalizeInput struct {
	OrderID    string
	Outcome    Outcome
	ReasonCode string
	DecidedAt  time.Time
}
