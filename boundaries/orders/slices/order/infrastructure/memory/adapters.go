package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/placeorder"
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
	if previous != nil && previous.CourierID != next.CourierID {
		delete(a.grants, grant{subject: application.Subject{Kind: application.SubjectUser, ID: previous.CourierID}, permission: getorder.PermissionView, resource: resource})
	}
	a.grants[grant{subject: application.Subject{Kind: application.SubjectUser, ID: next.CourierID}, permission: getorder.PermissionView, resource: resource}] = struct{}{}
	a.writes++
	return application.AuthorizationRevision(fmt.Sprintf("memory:%s:courier:%d", orderID, a.writes)), nil
}

// CourierWrites counts AssignCourier writes.
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

// Catalog serves configured restaurant snapshots.
type Catalog struct {
	mu          sync.Mutex
	restaurants map[order.RestaurantID]placeorder.CatalogSnapshot
	failure     error
	calls       int
}

func NewCatalog() *Catalog {
	return &Catalog{restaurants: make(map[order.RestaurantID]placeorder.CatalogSnapshot)}
}

func (c *Catalog) Put(restaurantID order.RestaurantID, snapshot placeorder.CatalogSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.restaurants[restaurantID] = snapshot
}

func (c *Catalog) Fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failure = err
}

func (c *Catalog) Snapshot(_ context.Context, restaurantID order.RestaurantID, menuItemIDs []order.MenuItemID) (placeorder.CatalogSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.failure != nil {
		return placeorder.CatalogSnapshot{}, c.failure
	}
	snapshot, ok := c.restaurants[restaurantID]
	if !ok {
		return placeorder.CatalogSnapshot{}, fmt.Errorf("%w: %q", placeorder.ErrUnknownRestaurant, restaurantID)
	}
	requested := placeorder.CatalogSnapshot{
		Currency:     snapshot.Currency,
		Items:        make(map[order.MenuItemID]placeorder.CatalogItem, len(menuItemIDs)),
		MinimumTotal: snapshot.MinimumTotal,
		MaximumTotal: snapshot.MaximumTotal,
	}
	for _, id := range menuItemIDs {
		if item, known := snapshot.Items[id]; known {
			requested.Items[id] = item
		}
	}
	return requested, nil
}

func (c *Catalog) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// Workflow records every call to the fulfillment port. Orders marked closed
// behave like a finished workflow.
type Workflow struct {
	mu            sync.Mutex
	starts        []fulfillment.StartRequest
	notifications []fulfillment.Notification
	cancellations []fulfillment.CancellationRequest
	closed        map[order.ID]struct{}
	startError    error
	notifyError   error
	cancelError   error
}

func NewWorkflow() *Workflow {
	return &Workflow{closed: make(map[order.ID]struct{})}
}

func (w *Workflow) Close(orderID order.ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed[orderID] = struct{}{}
}

func (w *Workflow) FailStart(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startError = err
}

func (w *Workflow) FailNotify(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.notifyError = err
}

func (w *Workflow) FailCancellation(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancelError = err
}

func (w *Workflow) Start(_ context.Context, request fulfillment.StartRequest) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.startError != nil {
		return w.startError
	}
	w.starts = append(w.starts, request)
	return nil
}

func (w *Workflow) Notify(_ context.Context, notification fulfillment.Notification) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.notifyError != nil {
		return w.notifyError
	}
	if _, closed := w.closed[notification.OrderID]; closed {
		return fulfillment.ErrWorkflowNotFound
	}
	w.notifications = append(w.notifications, notification)
	return nil
}

func (w *Workflow) RequestCancellation(_ context.Context, request fulfillment.CancellationRequest) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancelError != nil {
		return w.cancelError
	}
	if _, closed := w.closed[request.OrderID]; closed {
		return fulfillment.ErrWorkflowNotFound
	}
	w.cancellations = append(w.cancellations, request)
	return nil
}

func (w *Workflow) Starts() []fulfillment.StartRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]fulfillment.StartRequest(nil), w.starts...)
}

func (w *Workflow) Notifications() []fulfillment.Notification {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]fulfillment.Notification(nil), w.notifications...)
}

func (w *Workflow) Cancellations() []fulfillment.CancellationRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]fulfillment.CancellationRequest(nil), w.cancellations...)
}

// Clock is a manually advanced application.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func NewClock(now time.Time) *Clock { return &Clock{now: now} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// IDs generates sequential order identifiers order-1, order-2, ...
type IDs struct {
	mu   sync.Mutex
	next int
}

func (g *IDs) NewOrderID(context.Context) (order.ID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return order.ID(fmt.Sprintf("order-%d", g.next)), nil
}

// AlwaysOpen is a cancellation policy without a window; it is a test stand-in,
// not a product decision.
type AlwaysOpen struct{}

func (AlwaysOpen) Window(context.Context, time.Time) (order.CancellationWindow, error) {
	return order.CancellationWindow{ClosesAt: time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)}, nil
}

// FixedWindow opens the cancellation window at placement for Duration.
type FixedWindow struct {
	Duration time.Duration
}

func (w FixedWindow) Window(_ context.Context, placedAt time.Time) (order.CancellationWindow, error) {
	return order.CancellationWindow{OpensAt: placedAt, ClosesAt: placedAt.Add(w.Duration)}, nil
}
