// Package errorkind classifies the errors of the order slice use cases into
// errorkind.Kind. It is the only place that knows which application or
// domain error belongs to which kind: inbound adapters (gRPC, Kafka consumer,
// telemetry) and the composition root for the port of the fulfillment slice
// translate kinds with their own tables, so a
// new domain error is assigned a kind here and every adapter handles it the
// same way (FR-545).
package errorkind

import (
	"errors"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/slices/order/applications/failure"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/place"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// Kind is the class of a use case error that inbound adapters translate with
// their own tables: gRPC codes, the consumer error class, errors of a port of
// another slice and metric labels. Each adapter table covers every kind.
type Kind string

const (
	// InvalidInput: the Command, Query or a value in it is malformed.
	InvalidInput Kind = "invalid_input"
	// NotFound: an addressed resource does not exist.
	NotFound Kind = "not_found"
	// AlreadyExists: the resource to create exists already.
	AlreadyExists Kind = "already_exists"
	// Conflict: a concurrent change won; the call may be retried.
	Conflict Kind = "conflict"
	// DomainRule: the domain refused the operation in the current state.
	DomainRule Kind = "domain_rule"
	// Unavailable: a dependency did not answer; the call may be retried.
	Unavailable Kind = "unavailable"
	// Permission: the caller may not perform the operation.
	Permission Kind = "permission"
	// Internal: any other failure.
	Internal Kind = "internal"
)

// All lists every kind; adapter tests check their tables against it.
func All() []Kind {
	return []Kind{InvalidInput, NotFound, AlreadyExists, Conflict, DomainRule, Unavailable, Permission, Internal}
}

// Classify returns the kind of a non-nil use case error. An unavailable
// dependency is checked first: fail-closed authorization wraps it into a
// permission denial, and the caller may retry it.
func Classify(err error) Kind {
	var (
		invalidTransition order.InvalidTransition
		guard             order.GuardFailed
		notOrderable      order.NotOrderable
		notCancellable    order.CancellationNotAllowed
		slotUnavailable   place.SlotUnavailable
	)
	switch {
	case errors.Is(err, failure.ErrDependencyUnavailable), errors.Is(err, fulfillment.ErrWorkflowNotFound):
		return Unavailable
	case errors.Is(err, failure.ErrInvalidInput), errors.Is(err, authz.ErrInvalidSubject), errors.Is(err, vo.ErrInvalidValue):
		return InvalidInput
	case errors.As(err, &invalidTransition), errors.As(err, &guard),
		errors.As(err, &notOrderable), errors.As(err, &notCancellable),
		errors.Is(err, order.ErrOrderRequiresItems), errors.Is(err, order.ErrDuplicateMenuItem),
		errors.Is(err, order.ErrInvalidCancellationReason),
		errors.Is(err, place.ErrRestaurantNotAcceptingOrders), errors.Is(err, place.ErrPremiumUnavailable),
		errors.As(err, &slotUnavailable), errors.Is(err, place.ErrScheduledOrdersUnavailable),
		errors.Is(err, order.ErrNotScheduled),
		errors.Is(err, fulfillment.ErrCancellationRefused):
		return DomainRule
	case errors.Is(err, authz.ErrPermissionDenied):
		return Permission
	case errors.Is(err, order.ErrOrderNotFound), errors.Is(err, place.ErrUnknownRestaurant):
		return NotFound
	case errors.Is(err, order.ErrOrderAlreadyExists):
		return AlreadyExists
	case errors.Is(err, failure.ErrConcurrencyConflict):
		return Conflict
	default:
		return Internal
	}
}
