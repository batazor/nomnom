package memory

import (
	"context"
	"fmt"
	"sync"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/requestordercancellation"
	"nomnom/orders/slices/order/domains/order"
)

type grant struct {
	subject    application.Subject
	permission application.Permission
	resource   application.Resource
}

// Authorizer is a deny-by-default stand-in for the SpiceDB adapter. The Customer
// relationship of an Order grants view and cancel on that Order.
type Authorizer struct {
	mu         sync.Mutex
	grants     map[grant]struct{}
	checks     []application.AccessCheck
	checkError error
	writeError error
	// restaurants are the written order#restaurant relationships.
	restaurants map[order.ID]order.RestaurantID
	writes      int
}

func NewAuthorizer() *Authorizer {
	return &Authorizer{grants: make(map[grant]struct{}), restaurants: make(map[order.ID]order.RestaurantID)}
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

// FailWrites makes every relationship write return err.
func (a *Authorizer) FailWrites(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.writeError = err
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

func (a *Authorizer) GrantOrderRelationships(_ context.Context, orderID order.ID, customerID order.CustomerID, restaurantID order.RestaurantID) (application.AuthorizationRevision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.writeError != nil {
		return "", a.writeError
	}
	a.restaurants[orderID] = restaurantID
	customer := application.Subject{Kind: application.SubjectUser, ID: string(customerID)}
	resource := application.Resource{Type: getorder.ResourceOrder, ID: string(orderID)}
	a.grants[grant{subject: customer, permission: getorder.PermissionView, resource: resource}] = struct{}{}
	a.grants[grant{subject: customer, permission: requestordercancellation.PermissionCancel, resource: resource}] = struct{}{}
	return application.AuthorizationRevision("memory:" + string(orderID)), nil
}

// AssignCourier grants view on the Order to the next courier and revokes it
// from the previous one, as order#courier would.
func (a *Authorizer) AssignCourier(_ context.Context, orderID order.ID, next recordcourierassignment.Assignment, previous *recordcourierassignment.Assignment) (application.AuthorizationRevision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.writeError != nil {
		return "", a.writeError
	}
	resource := application.Resource{Type: getorder.ResourceOrder, ID: string(orderID)}
	if previous != nil && previous.CourierID != "" && previous.CourierID != next.CourierID {
		delete(a.grants, grant{subject: application.Subject{Kind: application.SubjectUser, ID: previous.CourierID}, permission: getorder.PermissionView, resource: resource})
	}
	a.grants[grant{subject: application.Subject{Kind: application.SubjectUser, ID: next.CourierID}, permission: getorder.PermissionView, resource: resource}] = struct{}{}
	a.writes++
	return application.AuthorizationRevision(fmt.Sprintf("memory:%s:courier:%d", orderID, a.writes)), nil
}

// ReleaseCourier revokes view on the Order from the released courier, as a
// DELETE of order#courier would; order#delivery is not modelled.
func (a *Authorizer) ReleaseCourier(_ context.Context, orderID order.ID, released recordcourierassignment.Assignment, _ bool) (application.AuthorizationRevision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.writeError != nil {
		return "", a.writeError
	}
	resource := application.Resource{Type: getorder.ResourceOrder, ID: string(orderID)}
	delete(a.grants, grant{subject: application.Subject{Kind: application.SubjectUser, ID: released.CourierID}, permission: getorder.PermissionView, resource: resource})
	a.writes++
	return application.AuthorizationRevision(fmt.Sprintf("memory:%s:courier:%d", orderID, a.writes)), nil
}

// CourierWrites counts AssignCourier and ReleaseCourier writes.
func (a *Authorizer) CourierWrites() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writes
}

// RestaurantOf returns the written order#restaurant subject.
func (a *Authorizer) RestaurantOf(orderID order.ID) order.RestaurantID {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.restaurants[orderID]
}

func (a *Authorizer) Checks() []application.AccessCheck {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]application.AccessCheck(nil), a.checks...)
}
