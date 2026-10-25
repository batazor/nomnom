// Package observability names the use cases of the order slice in telemetry and
// classifies their errors for the pkg/telemetry decorator. Composition roots
// decorate handlers with it; use cases do not know it exists.
package observability

import (
	"errors"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/errorkind"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
)

// Use case names are metric label values: renaming one breaks dashboards.
var (
	PlaceOrder               = telemetry.Command("place_order")
	RequestOrderCancellation = telemetry.Command("request_order_cancellation")
	ConfirmOrder             = telemetry.Command("confirm_order")
	RejectOrder              = telemetry.Command("reject_order")
	RecordPreparationStarted = telemetry.Command("record_preparation_started")
	RecordOrderReady         = telemetry.Command("record_order_ready")
	RelayPreparationIssue    = telemetry.Command("relay_preparation_issue")
	RecordCourierAssignment  = telemetry.Command("record_courier_assignment")
	RecordCourierRelease     = telemetry.Command("record_courier_release")
	RecordPickup             = telemetry.Command("record_pickup")
	RecordDeliveryCompletion = telemetry.Command("record_delivery_completion")
	RecordDeliveryFailure    = telemetry.Command("record_delivery_failure")
	CompleteOrder            = telemetry.Command("complete_order")
	FailOrCancelOrder        = telemetry.Command("fail_or_cancel_order")
	GetOrder                 = telemetry.Query("get_order")
	ListCustomerOrders       = telemetry.Query("list_customer_orders")
)

// Error types of the order slice, in addition to the common ones of pkg/telemetry.
const (
	ErrorTypeInvalidInput        = "invalid_input"
	ErrorTypePermissionDenied    = "permission_denied"
	ErrorTypeNotFound            = "not_found"
	ErrorTypeAlreadyExists       = "already_exists"
	ErrorTypeInvalidTransition   = "invalid_transition"
	ErrorTypeGuardFailed         = "guard_failed"
	ErrorTypeDomainRule          = "domain_rule"
	ErrorTypeCancellationRefused = "cancellation_refused"
	ErrorTypeConcurrencyConflict = "concurrency_conflict"
	// ErrorTypeDependencyUnavailable: SpiceDB, the catalog or the workflow did
	// not answer, also behind a fail-closed authorization check.
	ErrorTypeDependencyUnavailable = "dependency_unavailable"
)

// UseCases returns the instrumentation of the order slice.
func UseCases() *telemetry.UseCases { return telemetry.NewUseCases(Classify) }

func rejected(errorType string) telemetry.Failure {
	return telemetry.Failure{Outcome: telemetry.OutcomeRejected, ErrorType: errorType}
}

// failureByKind reports every application.ErrorKind: refusals by the rules of
// a use case are rejected, technical failures are errors.
var failureByKind = map[application.ErrorKind]telemetry.Failure{
	application.KindInvalidInput:  rejected(ErrorTypeInvalidInput),
	application.KindNotFound:      rejected(ErrorTypeNotFound),
	application.KindAlreadyExists: rejected(ErrorTypeAlreadyExists),
	application.KindDomainRule:    rejected(ErrorTypeDomainRule),
	application.KindPermission:    rejected(ErrorTypePermissionDenied),
	application.KindConflict:      {Outcome: telemetry.OutcomeError, ErrorType: ErrorTypeConcurrencyConflict},
	application.KindUnavailable:   {Outcome: telemetry.OutcomeError, ErrorType: ErrorTypeDependencyUnavailable},
	application.KindInternal:      {Outcome: telemetry.OutcomeError, ErrorType: telemetry.ErrorTypeInternal},
}

// Classify reports a use case error by its kind (errorkind.Classify). A domain
// refusal keeps its finer error type: an FSM transition, a guard or a
// Cancellation Request refused by the workflow.
func Classify(err error) telemetry.Failure {
	failure, ok := failureByKind[errorkind.Classify(err)]
	if !ok {
		return telemetry.Failure{Outcome: telemetry.OutcomeError, ErrorType: telemetry.ErrorTypeInternal}
	}
	var invalid order.InvalidTransition
	var guard order.GuardFailed
	switch {
	case failure.ErrorType != ErrorTypeDomainRule:
	case errors.As(err, &invalid):
		failure.ErrorType = ErrorTypeInvalidTransition
	case errors.As(err, &guard):
		failure.ErrorType = ErrorTypeGuardFailed
	case errors.Is(err, fulfillment.ErrCancellationRefused):
		failure.ErrorType = ErrorTypeCancellationRefused
	}
	return failure
}
