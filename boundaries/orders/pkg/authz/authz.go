// Package authz contains the verified caller identity and the authorization
// ports shared by the slices of the orders boundary. It holds no business rules.
package authz

import (
	"errors"
	"fmt"

	"nomnom/orders/pkg/usecase"
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
type Subject struct {
	Kind SubjectKind
	ID   string
}

func (s Subject) Validate() error {
	if s.Kind != SubjectUser && s.Kind != SubjectService {
		return usecase.InvalidInput("subject", "unknown kind")
	}
	if s.ID == "" {
		return usecase.InvalidInput("subject", "id is required")
	}
	return nil
}

func (s Subject) String() string { return string(s.Kind) + ":" + s.ID }

// IsUser reports whether the subject is the user id: the owner of a resource
// whose owner domain storage knows, for example the Customer of an Order.
func (s Subject) IsUser(id string) bool { return s.Kind == SubjectUser && s.ID == id }

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
