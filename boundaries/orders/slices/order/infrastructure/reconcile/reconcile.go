// Package reconcile keeps the order relationships of the authorization graph
// (order#customer, order#restaurant, order#courier, order#delivery) consistent
// with the Orders in PostgreSQL (docs/security.md, relationship lifecycle): it
// deletes orphan and stale relationships after a grace period and restores
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

const resourceOrder = "order"

// Relation is one reconciled relation of order and the object type of its subject.
type Relation struct {
	Name        postgres.Relation
	SubjectType string
}

// Relations are the order relations written by orders, in reconciliation order.
var Relations = []Relation{
	{Name: postgres.RelationCustomer, SubjectType: "user"},
	{Name: postgres.RelationRestaurant, SubjectType: "restaurant"},
	{Name: postgres.RelationCourier, SubjectType: "user"},
	{Name: postgres.RelationDelivery, SubjectType: "delivery"},
}

// Orders is the business side of the reconciliation.
type Orders interface {
	SubjectsOf(ctx context.Context, relation postgres.Relation, ids []string) (map[string]string, error)
	RelationPage(ctx context.Context, relation postgres.Relation, afterID string, limit int) ([]postgres.RelationSubject, error)
	RememberRevision(ctx context.Context, orderID order.ID, revision application.AuthorizationRevision) error
}

// Stats are the drift counters of one pass over all relations.
type Stats struct {
	Checked         int
	OrphansPending  int
	OrphansDeleted  int
	MissingRepaired int
	// Unrepairable counts Orders whose ids SpiceDB cannot store; their writers
	// could not have written a relationship either.
	Unrepairable int
}

// objectID is the SpiceDB object id grammar.
var objectID = regexp.MustCompile(`^[a-zA-Z0-9/_|\-=+]+$`)

// Reconciler remembers orphan candidates between passes: a relationship is
// deleted only when it stayed orphan for at least grace, because placement and
// the courier assignment consumer write it before their transaction commits.
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

// Reconcile runs one pass over every relation. A candidate of a relation that
// is no longer orphan is forgotten by the pass of that relation.
func (r *Reconciler) Reconcile(ctx context.Context) (Stats, error) {
	var stats Stats
	orphans := map[string]bool{}
	for _, relation := range Relations {
		if err := r.reconcile(ctx, relation, &stats, orphans); err != nil {
			return stats, err
		}
	}
	for key := range r.candidates {
		if !orphans[key] {
			delete(r.candidates, key)
		}
	}
	return stats, nil
}

// reconcile runs graph → business (orphans), then business → graph (missing)
// for one relation.
func (r *Reconciler) reconcile(ctx context.Context, relation Relation, stats *Stats, orphans map[string]bool) error {
	present := map[string]bool{} // "order:subject" of every relationship seen

	var cursor *v1.Cursor
	for {
		page, next, err := r.readPage(ctx, relation, cursor)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		ids := make([]string, 0, len(page))
		for _, rel := range page {
			ids = append(ids, rel.GetResource().GetObjectId())
		}
		subjects, err := r.orders.SubjectsOf(ctx, relation.Name, ids)
		if err != nil {
			return err
		}
		for _, rel := range page {
			stats.Checked++
			id, subject := rel.GetResource().GetObjectId(), rel.GetSubject().GetObject().GetObjectId()
			present[id+":"+subject] = true
			if stored, ok := subjects[id]; ok && stored == subject {
				continue
			}
			key := string(relation.Name) + ":" + id + ":" + subject
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
			if err := r.write(ctx, relation, v1.RelationshipUpdate_OPERATION_DELETE, id, subject); err != nil {
				return err
			}
			delete(r.candidates, key)
			stats.OrphansDeleted++
		}
		if next == nil || len(page) < r.pageSize {
			break
		}
		cursor = next
	}

	after := ""
	for {
		page, err := r.orders.RelationPage(ctx, relation.Name, after, r.pageSize)
		if err != nil {
			return err
		}
		for _, row := range page {
			if present[row.OrderID+":"+row.SubjectID] {
				continue
			}
			if !objectID.MatchString(row.OrderID) || !objectID.MatchString(row.SubjectID) {
				log.Printf("reconcile: order %q with %s %q has an id SpiceDB cannot store", row.OrderID, relation.Name, row.SubjectID)
				stats.Unrepairable++
				continue
			}
			revision, err := r.touch(ctx, relation, row.OrderID, row.SubjectID)
			if err != nil {
				return err
			}
			if err := r.orders.RememberRevision(ctx, order.ID(row.OrderID), revision); err != nil {
				return err
			}
			stats.MissingRepaired++
		}
		if len(page) < r.pageSize {
			return nil
		}
		after = page[len(page)-1].OrderID
	}
}

func (r *Reconciler) readPage(ctx context.Context, relation Relation, cursor *v1.Cursor) ([]*v1.Relationship, *v1.Cursor, error) {
	stream, err := r.permissions.ReadRelationships(ctx, &v1.ReadRelationshipsRequest{
		Consistency:        &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
		RelationshipFilter: &v1.RelationshipFilter{ResourceType: resourceOrder, OptionalRelation: string(relation.Name)},
		OptionalLimit:      uint32(r.pageSize), //nolint:gosec // page size is a small positive configuration value.
		OptionalCursor:     cursor,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read order#%s relationships: %w", relation.Name, err)
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
			return nil, nil, fmt.Errorf("read order#%s relationships: %w", relation.Name, err)
		}
		page = append(page, resp.GetRelationship())
		next = resp.GetAfterResultCursor()
	}
}

func (r *Reconciler) touch(ctx context.Context, relation Relation, orderID, subject string) (application.AuthorizationRevision, error) {
	resp, err := r.permissions.WriteRelationships(ctx, writeRequest(relation, v1.RelationshipUpdate_OPERATION_TOUCH, orderID, subject))
	if err != nil {
		return "", fmt.Errorf("restore order:%s#%s: %w", orderID, relation.Name, err)
	}
	return application.AuthorizationRevision(resp.GetWrittenAt().GetToken()), nil
}

func (r *Reconciler) write(ctx context.Context, relation Relation, op v1.RelationshipUpdate_Operation, orderID, subject string) error {
	if _, err := r.permissions.WriteRelationships(ctx, writeRequest(relation, op, orderID, subject)); err != nil {
		return fmt.Errorf("delete order:%s#%s@%s:%s: %w", orderID, relation.Name, relation.SubjectType, subject, err)
	}
	return nil
}

func writeRequest(relation Relation, op v1.RelationshipUpdate_Operation, orderID, subject string) *v1.WriteRelationshipsRequest {
	return &v1.WriteRelationshipsRequest{Updates: []*v1.RelationshipUpdate{{
		Operation: op,
		Relationship: &v1.Relationship{
			Resource: &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: orderID},
			Relation: string(relation.Name),
			Subject:  &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: relation.SubjectType, ObjectId: subject}},
		},
	}}}
}
