package fake

import (
	"context"
	"fmt"
	"sync"

	"nomnom/orders/pkg/application"
)

type grant struct {
	subject    application.Subject
	permission application.Permission
	resource   application.Resource
}

// Authorizer is a deny-by-default stand-in for the permission checks of the
// SpiceDB adapter; relationships are written by Sync Order Relationships.
type Authorizer struct {
	mu         sync.Mutex
	grants     map[grant]struct{}
	checks     []application.AccessCheck
	checkError error
}

func NewAuthorizer() *Authorizer {
	return &Authorizer{grants: make(map[grant]struct{})}
}

func (a *Authorizer) Allow(subject application.Subject, permission application.Permission, resource application.Resource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.grants[grant{subject: subject, permission: permission, resource: resource}] = struct{}{}
}

// FailChecks makes every Check return err, as an unavailable policy engine would.
func (a *Authorizer) FailChecks(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkError = err
}

func (a *Authorizer) Check(_ context.Context, check application.AccessCheck) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checks = append(a.checks, check)
	if a.checkError != nil {
		return a.checkError
	}
	if _, ok := a.grants[grant{subject: check.Subject, permission: check.Permission, resource: check.Resource}]; ok {
		return nil
	}
	return fmt.Errorf("%w: %s %s on %s:%s", application.ErrPermissionDenied,
		check.Subject, check.Permission, check.Resource.Type, check.Resource.ID)
}

func (a *Authorizer) Checks() []application.AccessCheck {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]application.AccessCheck(nil), a.checks...)
}
