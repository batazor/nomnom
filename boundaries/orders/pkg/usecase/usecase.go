// Package usecase contains the technical contract of a use case shared by the
// slices of the orders boundary: its inbound port and errors. It holds no
// business rules.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidInput marks a Command or Query rejected before any domain call.
	ErrInvalidInput = errors.New("invalid input")
	// ErrConcurrencyConflict marks a save against a stale aggregate version.
	ErrConcurrencyConflict = errors.New("concurrency conflict")
	// ErrDependencyUnavailable marks a dependency that did not answer: SpiceDB,
	// the restaurant catalog or the fulfillment workflow. The call may be retried.
	ErrDependencyUnavailable = errors.New("dependency unavailable")
)

// InvalidInput wraps ErrInvalidInput with the offending field.
func InvalidInput(field, reason string) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalidInput, field, reason)
}

// Handler is the inbound port of one use case. Adapters depend on it instead of
// the concrete handler, so the composition root can decorate use cases.
type Handler[In, Out any] interface {
	Handle(ctx context.Context, in In) (Out, error)
}

// Clock supplies the current time to use cases; domain code never reads it.
type Clock interface {
	Now() time.Time
}
