// Package application contains technical application ports and errors shared by
// every slice of the orders boundary. It holds no business rules.
package application

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidInput marks a Command or Query rejected before any domain call.
	ErrInvalidInput = errors.New("invalid input")
	// ErrPermissionDenied marks a denied or failed authoritative authorization check.
	ErrPermissionDenied = errors.New("permission denied")
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

// FailClosed normalizes an authorization result: any failure of the check
// denies the operation while keeping the original cause for diagnostics.
func FailClosed(err error) error {
	if err == nil || errors.Is(err, ErrPermissionDenied) {
		return err
	}
	return fmt.Errorf("%w: authorization check failed: %w", ErrPermissionDenied, err)
}

// Handler is the inbound port of one use case. Adapters depend on it instead of
// the concrete handler, so the composition root can decorate use cases.
type Handler[In, Out any] interface {
	Handle(ctx context.Context, in In) (Out, error)
}

// UnitOfWork is the local transaction boundary of a use case. A nested Do joins
// the active transaction; only the outermost scope commits or rolls back.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Inbox records a processed integration event inside the active transaction.
// Accept returns false when the consumer has already processed eventID.
type Inbox interface {
	Accept(ctx context.Context, consumer, eventID string) (bool, error)
}

// Clock supplies the current time to use cases; domain code never reads it.
type Clock interface {
	Now() time.Time
}

type SubjectKind string

const (
	SubjectUser    SubjectKind = "user"
	SubjectService SubjectKind = "service"
)

// Subject is the verified caller identity handed over by an inbound adapter.
type Subject struct {
	Kind SubjectKind
	ID   string
}

func (s Subject) Validate() error {
	if s.Kind != SubjectUser && s.Kind != SubjectService {
		return InvalidInput("subject", "unknown kind")
	}
	if s.ID == "" {
		return InvalidInput("subject", "id is required")
	}
	return nil
}

func (s Subject) String() string { return string(s.Kind) + ":" + s.ID }

type Permission string

type ResourceType string

// Resource is an authorization resource reference, for example order:<id>.
type Resource struct {
	Type ResourceType
	ID   string
}

// AccessCheck asks whether Subject may perform Permission on Resource.
type AccessCheck struct {
	Subject    Subject
	Permission Permission
	Resource   Resource
}

// AuthorizationRevision is the opaque revision (SpiceDB ZedToken) of a
// relationship write; checks of the resource read at least as fresh as it.
type AuthorizationRevision string
