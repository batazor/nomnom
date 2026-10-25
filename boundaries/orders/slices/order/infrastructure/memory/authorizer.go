package memory

import (
	"context"
	"fmt"
	"sync"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/getorder"
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
	// deliveries are the written order#delivery relationships.
	deliveries map[order.ID]string
	writes     int
}

func NewAuthorizer() *Authorizer {
	return &Authorizer{
		grants:      make(map[grant]struct{}),
		restaurants: make(map[order.ID]order.RestaurantID),
		deliveries:  make(map[order.ID]string),
	}
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

// LinkDelivery writes order#delivery of deliveryID and deletes that of
// the displaced one, as one SpiceDB request would: an Order has one relation.
func (a *Authorizer) LinkDelivery(_ context.Context, orderID order.ID, deliveryID, _ string) (application.AuthorizationRevision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.writeError != nil {
		return "", a.writeError
	}
	a.deliveries[orderID] = deliveryID
	a.writes++
	return application.AuthorizationRevision(fmt.Sprintf("memory:%s:delivery:%d", orderID, a.writes)), nil
}

// UnlinkDelivery deletes order#delivery of deliveryID.
func (a *Authorizer) UnlinkDelivery(_ context.Context, orderID order.ID, deliveryID string) (application.AuthorizationRevision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.writeError != nil {
		return "", a.writeError
	}
	if a.deliveries[orderID] == deliveryID {
		delete(a.deliveries, orderID)
	}
	a.writes++
	return application.AuthorizationRevision(fmt.Sprintf("memory:%s:delivery:%d", orderID, a.writes)), nil
}

// DeliveryOf returns the written order#delivery subject of an Order.
func (a *Authorizer) DeliveryOf(orderID order.ID) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	deliveryID, ok := a.deliveries[orderID]
	return deliveryID, ok
}

// DeliveryWrites counts LinkDelivery and UnlinkDelivery requests.
func (a *Authorizer) DeliveryWrites() int {
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
