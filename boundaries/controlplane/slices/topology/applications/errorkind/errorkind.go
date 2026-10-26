// Package errorkind classifies the errors of the topology use cases into
// application.ErrorKind. Inbound adapters translate kinds with their own
// tables; a new domain error is assigned a kind here.
package errorkind

import (
	"errors"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	marketvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

// Classify returns the kind of a non-nil use case error. An unavailable
// dependency is checked first: fail-closed authorization wraps it into a
// permission denial, and the caller may retry it.
func Classify(err error) application.ErrorKind {
	switch {
	case errors.Is(err, application.ErrDependencyUnavailable):
		return application.KindUnavailable
	case errors.Is(err, application.ErrInvalidInput), errors.Is(err, cellvo.ErrInvalidValue), errors.Is(err, marketvo.ErrInvalidValue):
		return application.KindInvalidInput
	case errors.Is(err, application.ErrPermissionDenied):
		return application.KindPermission
	case errors.Is(err, cell.ErrAlreadyRegistered), errors.Is(err, market.ErrAlreadyCreated):
		return application.KindAlreadyExists
	case errors.Is(err, market.ErrUnknownCell):
		return application.KindDomainRule
	default:
		return application.KindInternal
	}
}
