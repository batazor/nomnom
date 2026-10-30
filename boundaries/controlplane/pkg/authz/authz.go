// Package authz contains the verified caller identity and the authorization
// ports shared by the slices of the controlplane boundary. It holds no business rules.
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/usecase"
)

// ErrPermissionDenied marks a denied or failed authoritative authorization check.
var ErrPermissionDenied = errors.New("permission denied")

// FailClosed normalizes an authorization result: any failure of the check
// denies the operation while keeping the original cause for diagnostics.
func FailClosed(err error) error {
	if err == nil || errors.Is(err, ErrPermissionDenied) {
		return err
	}
	return fmt.Errorf("%w: authorization check failed: %w", ErrPermissionDenied, err)
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
		return usecase.InvalidInput("subject", "unknown kind")
	}
	if s.ID == "" {
		return usecase.InvalidInput("subject", "id is required")
	}
	if s.Service == "" {
		return usecase.InvalidInput("subject", "calling service is required")
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
