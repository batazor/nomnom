// Package observability names the use cases of the order slice in telemetry and
// classifies their errors for the pkg/telemetry decorator. Composition roots
// decorate handlers with it; use cases do not know it exists.
package observability

import (
	"errors"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
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
)

// UseCases returns the instrumentation of the order slice.
func UseCases() *telemetry.UseCases { return telemetry.NewUseCases(Classify) }

func rejected(errorType string) telemetry.Failure {
	return telemetry.Failure{Outcome: telemetry.OutcomeRejected, ErrorType: errorType}
}

// Classify separates refusals by the rules of a use case from technical
// failures. A failed authorization backend is reported as permission_denied,
// because application.FailClosed hides the cause by design.
func Classify(err error) telemetry.Failure {
	var invalid order.InvalidTransition
	var guard order.GuardFailed
	switch {
	case errors.Is(err, application.ErrInvalidInput), errors.Is(err, vo.ErrInvalidValue):
		return rejected(ErrorTypeInvalidInput)
	case errors.Is(err, application.ErrPermissionDenied):
		return rejected(ErrorTypePermissionDenied)
	case errors.Is(err, order.ErrOrderNotFound), errors.Is(err, placeorder.ErrUnknownRestaurant),
		errors.Is(err, fulfillment.ErrWorkflowNotFound):
		return rejected(ErrorTypeNotFound)
	case errors.Is(err, order.ErrOrderAlreadyExists):
		return rejected(ErrorTypeAlreadyExists)
	case errors.As(err, &invalid):
		return rejected(ErrorTypeInvalidTransition)
	case errors.As(err, &guard):
		return rejected(ErrorTypeGuardFailed)
	case errors.Is(err, order.ErrOrderRequiresItems), errors.Is(err, order.ErrDuplicateMenuItem),
		errors.Is(err, order.ErrInvalidCancellationReason):
		return rejected(ErrorTypeDomainRule)
	case errors.Is(err, fulfillment.ErrCancellationRefused):
		return rejected(ErrorTypeCancellationRefused)
	case errors.Is(err, application.ErrConcurrencyConflict):
		return telemetry.Failure{Outcome: telemetry.OutcomeError, ErrorType: ErrorTypeConcurrencyConflict}
	default:
		return telemetry.Failure{Outcome: telemetry.OutcomeError, ErrorType: telemetry.ErrorTypeInternal}
	}
}
