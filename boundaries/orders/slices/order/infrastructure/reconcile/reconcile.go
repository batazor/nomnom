// Package reconcile keeps the authorization graph order#customer consistent
// with the Orders in PostgreSQL (docs/security.md, relationship lifecycle):
// it deletes orphan and stale relationships after a grace period and restores
// missing ones. SpiceDB stays a derived graph; PostgreSQL is the source of truth.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

const (
	resourceOrder    = "order"
	relationCustomer = "customer"
	subjectUser      = "user"
)

// Orders is the business side of the reconciliation.
type Orders interface {
	CustomersOf(ctx context.Context, ids []string) (map[string]string, error)
	OrderCustomerPage(ctx context.Context, afterID string, limit int) ([]postgres.OrderCustomer, error)
	RememberRevision(ctx context.Context, orderID order.ID, revision application.AuthorizationRevision) error
}

// Stats are the drift counters of one pass.
type Stats struct {
	Checked         int
	OrphansPending  int
	OrphansDeleted  int
	MissingRepaired int
	// Unrepairable counts Orders whose id SpiceDB cannot store; their placement
	// could not have written a relationship either.
	Unrepairable int
}

// objectID is the SpiceDB object id grammar.
var objectID = regexp.MustCompile(`^[a-zA-Z0-9/_|\-=+]+$`)

// Reconciler remembers orphan candidates between passes: a relationship is
// deleted only when it stayed orphan for at least grace, because placement
// writes it before the Order commits.
type Reconciler struct {
	permissions v1.PermissionsServiceClient
	orders      Orders
	grace       time.Duration
	pageSize    int
	now         func() time.Time
	candidates  map[string]time.Time
}

func New(permissions v1.PermissionsServiceClient, orders Orders, grace time.Duration, pageSize int) *Reconciler {
	return &Reconciler{
		permissions: permissions, orders: orders, grace: grace, pageSize: pageSize,
		now: time.Now, candidates: map[string]time.Time{},
	}
}

// Reconcile runs one pass: graph → business (orphans), then business → graph (missing).
func (r *Reconciler) Reconcile(ctx context.Context) (Stats, error) {
	var stats Stats
	present := map[string]bool{} // "order:customer" of every relationship seen
	orphans := map[string]bool{}

	var cursor *v1.Cursor
	for {
		page, next, err := r.readPage(ctx, cursor)
		if err != nil {
			return stats, err
		}
		if len(page) == 0 {
			break
		}
		ids := make([]string, 0, len(page))
		for _, rel := range page {
			ids = append(ids, rel.GetResource().GetObjectId())
		}
		customers, err := r.orders.CustomersOf(ctx, ids)
		if err != nil {
			return stats, err
		}
		for _, rel := range page {
			stats.Checked++
			id, customer := rel.GetResource().GetObjectId(), rel.GetSubject().GetObject().GetObjectId()
			present[id+":"+customer] = true
			if stored, ok := customers[id]; ok && stored == customer {
				continue
			}
			key := id + ":" + customer
			orphans[key] = true
			first, seen := r.candidates[key]
			if !seen {
				r.candidates[key] = r.now()
				stats.OrphansPending++
				continue
			}
			if r.now().Sub(first) < r.grace {
				stats.OrphansPending++
				continue
			}
			if err := r.write(ctx, v1.RelationshipUpdate_OPERATION_DELETE, id, customer); err != nil {
				return stats, err
			}
			delete(r.candidates, key)
			stats.OrphansDeleted++
		}
		if next == nil || len(page) < r.pageSize {
			break
		}
		cursor = next
	}
	// A candidate that is no longer orphan (its Order committed) is forgotten.
	for key := range r.candidates {
		if !orphans[key] {
			delete(r.candidates, key)
		}
	}

	after := ""
	for {
		page, err := r.orders.OrderCustomerPage(ctx, after, r.pageSize)
		if err != nil {
			return stats, err
		}
		for _, row := range page {
			if present[row.OrderID+":"+row.CustomerID] {
				continue
			}
			if !objectID.MatchString(row.OrderID) || !objectID.MatchString(row.CustomerID) {
				log.Printf("reconcile: order %q of customer %q has an id SpiceDB cannot store", row.OrderID, row.CustomerID)
				stats.Unrepairable++
				continue
			}
			revision, err := r.touch(ctx, row.OrderID, row.CustomerID)
			if err != nil {
				return stats, err
			}
			if err := r.orders.RememberRevision(ctx, order.ID(row.OrderID), revision); err != nil {
				return stats, err
			}
			stats.MissingRepaired++
		}
		if len(page) < r.pageSize {
			return stats, nil
		}
		after = page[len(page)-1].OrderID
	}
}

func (r *Reconciler) readPage(ctx context.Context, cursor *v1.Cursor) ([]*v1.Relationship, *v1.Cursor, error) {
	stream, err := r.permissions.ReadRelationships(ctx, &v1.ReadRelationshipsRequest{
		Consistency:        &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
		RelationshipFilter: &v1.RelationshipFilter{ResourceType: resourceOrder, OptionalRelation: relationCustomer},
		OptionalLimit:      uint32(r.pageSize), //nolint:gosec // page size is a small positive configuration value.
		OptionalCursor:     cursor,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read order#customer relationships: %w", err)
	}
	var (
		page []*v1.Relationship
		next *v1.Cursor
	)
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return page, next, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read order#customer relationships: %w", err)
		}
		page = append(page, resp.GetRelationship())
		next = resp.GetAfterResultCursor()
	}
}

func (r *Reconciler) touch(ctx context.Context, orderID, customer string) (application.AuthorizationRevision, error) {
	resp, err := r.permissions.WriteRelationships(ctx, writeRequest(v1.RelationshipUpdate_OPERATION_TOUCH, orderID, customer))
	if err != nil {
		return "", fmt.Errorf("restore order:%s#customer: %w", orderID, err)
	}
	return application.AuthorizationRevision(resp.GetWrittenAt().GetToken()), nil
}

func (r *Reconciler) write(ctx context.Context, op v1.RelationshipUpdate_Operation, orderID, customer string) error {
	if _, err := r.permissions.WriteRelationships(ctx, writeRequest(op, orderID, customer)); err != nil {
		return fmt.Errorf("delete order:%s#customer@user:%s: %w", orderID, customer, err)
	}
	return nil
}

func writeRequest(op v1.RelationshipUpdate_Operation, orderID, customer string) *v1.WriteRelationshipsRequest {
	return &v1.WriteRelationshipsRequest{Updates: []*v1.RelationshipUpdate{{
		Operation: op,
		Relationship: &v1.Relationship{
			Resource: &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: orderID},
			Relation: relationCustomer,
			Subject:  &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: subjectUser, ObjectId: customer}},
		},
	}}}
}
