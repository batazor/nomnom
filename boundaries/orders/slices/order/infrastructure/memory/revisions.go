package memory

import (
	"context"
	"fmt"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/domains/order"
)

// RememberRevision keeps the authorization revision of an Order.
func (s *Store) RememberRevision(ctx context.Context, orderID order.ID, revision application.AuthorizationRevision) error {
	defer s.lock(ctx)()
	if _, ok := s.state.orders[orderID]; !ok {
		return fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	s.state.revisions[orderID] = revision
	return nil
}

// Revision returns the authorization revision of an Order, if any.
func (s *Store) Revision(ctx context.Context, orderID string) (application.AuthorizationRevision, bool, error) {
	defer s.lock(ctx)()
	revision, ok := s.state.revisions[order.ID(orderID)]
	return revision, ok, nil
}

// CourierAssignment returns the stored Courier Assignment of an Order, if any.
func (s *Store) CourierAssignment(ctx context.Context, orderID order.ID) (recordcourierassignment.Assignment, bool, error) {
	defer s.lock(ctx)()
	assignment, ok := s.state.assignments[orderID]
	return assignment, ok, nil
}

// RememberCourierAssignment replaces the Courier Assignment and the
// authorization revision of an Order.
func (s *Store) RememberCourierAssignment(ctx context.Context, orderID order.ID, assignment recordcourierassignment.Assignment, revision application.AuthorizationRevision) error {
	defer s.lock(ctx)()
	if _, ok := s.state.orders[orderID]; !ok {
		return fmt.Errorf("%w: %q", order.ErrOrderNotFound, orderID)
	}
	s.state.assignments[orderID] = assignment
	if revision != "" {
		s.state.revisions[orderID] = revision
	}
	return nil
}
