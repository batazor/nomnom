// Package temporal is the Temporal adapter of the Order Fulfillment process:
// the workflow, its Activities and the client that implements the application
// port fulfillment of the order slice. The workflow translates Signals and the
// Update into calls of the domain model fulfillment and executes its decisions
// as Activities and timers; it does not know the Order model, Commands or
// handlers, Activities and the client translate to them.
package temporal

import (
	"time"

	"go.temporal.io/sdk/workflow"

	"nomnom/orders/slices/fulfillment/applications/neighbours"
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

	UpdateCancellationRequested = "CancellationRequested"
)

// Activity names registered in the worker.
const (
	ActivityAuthorizePayment           = "AuthorizePayment"
	ActivityCapturePayment             = "CapturePayment"
	ActivityCancelPaymentAuthorization = "CancelPaymentAuthorization"
	ActivityRefundPayment              = "RefundPayment"
	ActivityRecordPaymentAuthorization = "RecordPaymentAuthorization"
	ActivityRequestRestaurantOrder     = "RequestRestaurantOrder"
	ActivityCancelRestaurantOrder      = "CancelRestaurantOrder"
	ActivityCreateDelivery             = "CreateDelivery"
	ActivityCancelDelivery             = "CancelDelivery"
	ActivityCompleteOrder              = "CompleteOrder"
	ActivityFailOrCancelOrder          = "FailOrCancelOrder"

	// Activities of the scheduled route (FR-682, FR-678).
	ActivityConfirmSlotReservation = "ConfirmSlotReservation"
	ActivityPlanSchedule           = "PlanSchedule"
	ActivityQueueRestaurantOrder   = "QueueRestaurantOrder"
	ActivityReleaseSlotReservation = "ReleaseSlotReservation"

	ActivityRecordRestaurantPickup = "RecordRestaurantPickup"
)

// The scheduled route is introduced under a Temporal patch (FR-682): a run
// takes it only when its history records this change, so histories started
// before it replay the ASAP route. The marker is written only for a Scheduled
// Order, and the ASAP route records no new command.
const (
	ScheduledRouteChangeID = "scheduled-route"
	ScheduledRouteVersion  = workflow.Version(1)
)

// The kitchen learns about the Pickup from the workflow under a Temporal patch:
// a run recorded before it replays without the step.
const (
	RestaurantPickupChangeID = "restaurant-pickup"
	RestaurantPickupVersion  = workflow.Version(1)
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
	// DeliveryWindow is the window of a Scheduled Order from OrderPlaced; it
	// chooses the scheduled route. Nil is an ASAP Order, as in every input
	// recorded before the field existed.
	DeliveryWindow *DeliveryWindow `json:",omitempty"`
}

// DeliveryWindow is the half-open interval [Start, End) of a Scheduled Order.
type DeliveryWindow struct {
	Start time.Time
	End   time.Time
}

// Fact is the payload of every Integration Event Signal.
type Fact struct {
	EventID    string
	OccurredAt time.Time
	Reason     string
	// ExpectedReadyAt is set only for PreparationDelayed.
	ExpectedReadyAt time.Time
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
	ReasonPickupTimeout               = fulfillment.ReasonPickupTimeout
	ReasonDeliveryFailed              = fulfillment.ReasonDeliveryFailed
	ReasonDeliveryTimeout             = fulfillment.ReasonDeliveryTimeout
	ReasonCustomerRequested           = fulfillment.ReasonCustomerRequested
	ReasonSlotUnavailable             = fulfillment.ReasonSlotUnavailable
)

// Stage is the Workflow Stage published as OrderFulfillmentStage.
type Stage = fulfillment.Stage

const (
	StageAuthorizingPayment = fulfillment.StageAuthorizingPayment
	StageWaitingAcceptance  = fulfillment.StageWaitingAcceptance
	StageScheduled          = fulfillment.StageScheduled
	StagePreparing          = fulfillment.StagePreparing
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
	// PlannedReadyAt is the planned_ready_at of the first plan of a Scheduled
	// Order, set for RequestRestaurantOrder and the replan PlanSchedule.
	PlannedReadyAt time.Time `json:",omitzero"`
	// RestaurantOrderID is set for QueueRestaurantOrder: the id returned by
	// RequestRestaurantOrder.
	RestaurantOrderID string `json:",omitempty"`
}

// RestaurantOrderRequested is the result of RequestRestaurantOrder.
type RestaurantOrderRequested struct {
	RestaurantOrderID string
}

// SlotDecision is the result of ConfirmSlotReservation: a window that filled
// up after its hold expired is a result, not an error.
type SlotDecision = neighbours.SlotDecision

// Plan is the result of PlanSchedule, kept in Event History.
type Plan = fulfillment.Plan

// PaymentDecision is the result of AuthorizePayment and CapturePayment: the
// business result of the payments port; a decline is a result, not an error.
type PaymentDecision = neighbours.PaymentDecision

// FinalizeInput carries the negative outcome chosen by the workflow.
type FinalizeInput struct {
	OrderID    string
	Outcome    Outcome
	ReasonCode string
	DecidedAt  time.Time
}
