// Package errorkind classifies the errors of the order slice use cases into
// application.ErrorKind. It is the only place that knows which application or
// domain error belongs to which kind: inbound adapters (gRPC, Kafka consumer,
// Temporal Activities, telemetry) translate kinds with their own tables, so a
// new domain error is assigned a kind here and every adapter handles it the
// same way (FR-545).
//
// The function lives in the slice rather than in pkg/application: the use
// case sentinels it reads (placeorder, fulfillment) import pkg/application.
package errorkind

import (
	"errors"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// Classify returns the kind of a non-nil use case error. An unavailable
// dependency is checked first: fail-closed authorization wraps it into a
// permission denial, and the caller may retry it.
func Classify(err error) application.ErrorKind {
	var (
		invalidTransition order.InvalidTransition
		guard             order.GuardFailed
		notOrderable      order.NotOrderable
		notCancellable    order.CancellationNotAllowed
		slotUnavailable   placeorder.SlotUnavailable
	)
	switch {
	case errors.Is(err, application.ErrDependencyUnavailable), errors.Is(err, fulfillment.ErrWorkflowNotFound):
		return application.KindUnavailable
	case errors.Is(err, application.ErrInvalidInput), errors.Is(err, vo.ErrInvalidValue):
		return application.KindInvalidInput
	case errors.As(err, &invalidTransition), errors.As(err, &guard),
		errors.As(err, &notOrderable), errors.As(err, &notCancellable),
		errors.Is(err, order.ErrOrderRequiresItems), errors.Is(err, order.ErrDuplicateMenuItem),
		errors.Is(err, order.ErrInvalidCancellationReason),
		errors.Is(err, placeorder.ErrRestaurantNotAcceptingOrders), errors.Is(err, placeorder.ErrPremiumUnavailable),
		errors.As(err, &slotUnavailable), errors.Is(err, placeorder.ErrScheduledOrdersUnavailable),
		errors.Is(err, order.ErrNotScheduled),
		errors.Is(err, fulfillment.ErrCancellationRefused):
		return application.KindDomainRule
	case errors.Is(err, application.ErrPermissionDenied):
		return application.KindPermission
	case errors.Is(err, order.ErrOrderNotFound), errors.Is(err, placeorder.ErrUnknownRestaurant):
		return application.KindNotFound
	case errors.Is(err, order.ErrOrderAlreadyExists):
		return application.KindAlreadyExists
	case errors.Is(err, application.ErrConcurrencyConflict):
		return application.KindConflict
	default:
		return application.KindInternal
	}
}
