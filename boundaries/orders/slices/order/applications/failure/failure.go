// Package failure contains the errors shared by the use cases and adapters of
// the order slice. It holds no business rules.
package failure

import (
	"errors"
	"fmt"
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
