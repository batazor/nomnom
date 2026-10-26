// Package application contains technical application ports and errors shared by
// the slices of the controlplane boundary. It holds no business rules.
package application

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrInvalidInput marks a Command or Query rejected before any domain call.
	ErrInvalidInput = errors.New("invalid input")
	// ErrPermissionDenied marks a denied or failed authoritative authorization check.
	ErrPermissionDenied = errors.New("permission denied")
	// ErrDependencyUnavailable marks a dependency that did not answer: SpiceDB or
	// PostgreSQL. The call may be retried.
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

type SubjectKind string

const (
	SubjectUser    SubjectKind = "user"
	SubjectService SubjectKind = "service"
)

// Subject is the verified caller identity handed over by an inbound adapter.
// Service is the authenticated calling workload (ADR 27): the subject itself
// for a service call, the delegate that asserted the user for a user call
// (ADR 32).
type Subject struct {
	Kind    SubjectKind
	ID      string
	Service string
}

func (s Subject) Validate() error {
	if s.Kind != SubjectUser && s.Kind != SubjectService {
		return InvalidInput("subject", "unknown kind")
	}
	if s.ID == "" {
		return InvalidInput("subject", "id is required")
	}
	if s.Service == "" {
		return InvalidInput("subject", "calling service is required")
	}
	return nil
}

func (s Subject) String() string { return string(s.Kind) + ":" + s.ID }

type Permission string

type ResourceType string

// Resource is an authorization resource reference, for example platform:nomnom.
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

// Authorizer answers an AccessCheck: nil grants, ErrPermissionDenied denies,
// any other error is a failed check that use cases fail closed on.
type Authorizer interface {
	Check(ctx context.Context, check AccessCheck) error
}

// ServiceAllowlist names the calling services allowed to use an operation that
// is granted to workloads rather than users (the Placement Map reads of BFF).
type ServiceAllowlist map[string]bool

// Allow grants the call when the authenticated calling service is in the list,
// whoever the asserted user is.
func (a ServiceAllowlist) Allow(subject Subject) error {
	if err := subject.Validate(); err != nil {
		return err
	}
	if !a[subject.Service] {
		return fmt.Errorf("%w: service %s may not read the topology", ErrPermissionDenied, subject.Service)
	}
	return nil
}
