// Package errorkind classifies the errors of the order slice use cases into
// usecase.ErrorKind. It is the only place that knows which application or
// domain error belongs to which kind: inbound adapters (gRPC, Kafka consumer,
// telemetry) and the composition root for the port of the fulfillment slice
// translate kinds with their own tables, so a
// new domain error is assigned a kind here and every adapter handles it the
// same way (FR-545).
//
// The function lives in the slice rather than in pkg/usecase: the use
// case sentinels it reads (place, fulfillment) import pkg/usecase.
package errorkind

import (
	"errors"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/place"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// Classify returns the kind of a non-nil use case error. An unavailable
// dependency is checked first: fail-closed authorization wraps it into a
// permission denial, and the caller may retry it.
func Classify(err error) usecase.ErrorKind {
	var (
		invalidTransition order.InvalidTransition
		guard             order.GuardFailed
		notOrderable      order.NotOrderable
		notCancellable    order.CancellationNotAllowed
		slotUnavailable   place.SlotUnavailable
	)
	switch {
	case errors.Is(err, usecase.ErrDependencyUnavailable), errors.Is(err, fulfillment.ErrWorkflowNotFound):
		return usecase.KindUnavailable
	case errors.Is(err, usecase.ErrInvalidInput), errors.Is(err, vo.ErrInvalidValue):
		return usecase.KindInvalidInput
	case errors.As(err, &invalidTransition), errors.As(err, &guard),
		errors.As(err, &notOrderable), errors.As(err, &notCancellable),
		errors.Is(err, order.ErrOrderRequiresItems), errors.Is(err, order.ErrDuplicateMenuItem),
		errors.Is(err, order.ErrInvalidCancellationReason),
		errors.Is(err, place.ErrRestaurantNotAcceptingOrders), errors.Is(err, place.ErrPremiumUnavailable),
		errors.As(err, &slotUnavailable), errors.Is(err, place.ErrScheduledOrdersUnavailable),
		errors.Is(err, order.ErrNotScheduled),
		errors.Is(err, fulfillment.ErrCancellationRefused):
		return usecase.KindDomainRule
	case errors.Is(err, authz.ErrPermissionDenied):
		return usecase.KindPermission
	case errors.Is(err, order.ErrOrderNotFound), errors.Is(err, place.ErrUnknownRestaurant):
		return usecase.KindNotFound
	case errors.Is(err, order.ErrOrderAlreadyExists):
		return usecase.KindAlreadyExists
	case errors.Is(err, usecase.ErrConcurrencyConflict):
		return usecase.KindConflict
	default:
		return usecase.KindInternal
	}
}
