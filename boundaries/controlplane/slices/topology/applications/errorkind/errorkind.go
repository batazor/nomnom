// Package errorkind classifies the errors of the topology use cases into
// usecase.ErrorKind. Inbound adapters translate kinds with their own
// tables; a new domain error is assigned a kind here.
package errorkind

import (
	"errors"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/authz"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/usecase"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	marketvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

// Classify returns the kind of a non-nil use case error. An unavailable
// dependency is checked first: fail-closed authorization wraps it into a
// permission denial, and the caller may retry it.
func Classify(err error) usecase.ErrorKind {
	switch {
	case errors.Is(err, usecase.ErrDependencyUnavailable):
		return usecase.KindUnavailable
	case errors.Is(err, usecase.ErrInvalidInput), errors.Is(err, cellvo.ErrInvalidValue), errors.Is(err, marketvo.ErrInvalidValue):
		return usecase.KindInvalidInput
	case errors.Is(err, authz.ErrPermissionDenied):
		return usecase.KindPermission
	case errors.Is(err, cell.ErrAlreadyRegistered), errors.Is(err, market.ErrAlreadyCreated), errors.Is(err, market.ErrArchived):
		return usecase.KindAlreadyExists
	case errors.Is(err, market.ErrUnknownCell):
		return usecase.KindDomainRule
	case errors.Is(err, market.ErrNotFound):
		return usecase.KindNotFound
	default:
		return usecase.KindInternal
	}
}
