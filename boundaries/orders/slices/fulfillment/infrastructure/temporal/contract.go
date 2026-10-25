// Package temporal is the Temporal adapter of the Order Fulfillment process:
// the workflow, its Activities and the client that implements the application
// port fulfillment of the order slice. The workflow translates Signals and the
// Update into calls of the domain model fulfillment and executes its decisions
// as Activities and timers; it does not know the Order model, Commands or
// handlers, Activities and the client translate to them.
package temporal

import (
	"time"

	"nomnom/orders/slices/fulfillment/domains/fulfillment"
)

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

type OperatorChoice = fulfillment.OperatorChoice

const (
	OperatorRetryAssignment = fulfillment.OperatorRetryAssignment
	OperatorCancel          = fulfillment.OperatorCancel
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

type Outcome = fulfillment.Outcome

const (
	OutcomeCompleted    = fulfillment.OutcomeCompleted
	OutcomeCancellation = fulfillment.OutcomeCancellation
	OutcomeFailure      = fulfillment.OutcomeFailure
)

// Reason codes of the negative outcome passed to FailOrCancelOrder.
const (
	ReasonPaymentDeclined             = fulfillment.ReasonPaymentDeclined
	ReasonPaymentCaptureDeclined      = fulfillment.ReasonPaymentCaptureDeclined
	ReasonRestaurantRejected          = fulfillment.ReasonRestaurantRejected
	ReasonRestaurantAcceptanceTimeout = fulfillment.ReasonRestaurantAcceptanceTimeout
	ReasonPreparationFailed           = fulfillment.ReasonPreparationFailed
	ReasonPreparationTimeout          = fulfillment.ReasonPreparationTimeout
	ReasonCourierAssignmentExhausted  = fulfillment.ReasonCourierAssignmentExhausted
	ReasonPickupTimeout               = fulfillment.ReasonPickupTimeout
	ReasonDeliveryFailed              = fulfillment.ReasonDeliveryFailed
	ReasonDeliveryTimeout             = fulfillment.ReasonDeliveryTimeout
	ReasonCustomerRequested           = fulfillment.ReasonCustomerRequested
	ReasonOperatorDecision            = fulfillment.ReasonOperatorDecision
)

// Stage is the Workflow Stage published as OrderFulfillmentStage.
type Stage = fulfillment.Stage

const (
	StageAuthorizingPayment = fulfillment.StageAuthorizingPayment
	StageWaitingAcceptance  = fulfillment.StageWaitingAcceptance
	StagePreparing          = fulfillment.StagePreparing
	StageWaitingOperator    = fulfillment.StageWaitingOperator
	StageWaitingPickup      = fulfillment.StageWaitingPickup
	StageDelivering         = fulfillment.StageDelivering
	StageCompleting         = fulfillment.StageCompleting
	StageCompensating       = fulfillment.StageCompensating
	StageCompleted          = fulfillment.StageCompleted
	StageCancelled          = fulfillment.StageCancelled
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
	// DecidedAt is the workflow time the step was decided at, set for
	// CompleteOrder: the completion moment of the Order, the same on every
	// Activity attempt.
	DecidedAt time.Time `json:",omitzero"`
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
