// Package spicedb implements the authorization ports of the order slice on
// SpiceDB (ADR 18): permission checks of the use cases, the creation-time
// order#customer and order#restaurant relationships and the order#courier and
// order#delivery relationships of the Courier Assignment.
// Schema: authorization/schema.zed.
package spicedb

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	authzed "github.com/authzed/authzed-go/v1"
	"github.com/authzed/grpcutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/domains/order"
)

// ErrUnavailable means SpiceDB did not answer; use cases fail closed on it.
var ErrUnavailable = fmt.Errorf("authorization service: %w", application.ErrDependencyUnavailable)

const (
	resourceOrder      = "order"
	relationCustomer   = "customer"
	relationRestaurant = "restaurant"
	relationCourier    = "courier"
	relationDelivery   = "delivery"
	subjectUser        = "user"
	subjectRestaurant  = "restaurant"
	subjectDelivery    = "delivery"
)

// RevisionReader returns the authorization revision stored with an Order.
type RevisionReader interface {
	Revision(ctx context.Context, orderID string) (application.AuthorizationRevision, bool, error)
}

// Authorizer checks application.AccessCheck as `<type>:<id>#<permission>` for
// `user:<id>` or `service:<id>`. An Order with a stored authorization revision
// (the ZedToken of its creation-time relationship) is checked at least as
// fresh as that write.
type Authorizer struct {
	permissions v1.PermissionsServiceClient
	revisions   RevisionReader
	timeout     time.Duration
}

// Dial connects lazily to SpiceDB with the preshared key.
func Dial(cfg config.SpiceDBConfig) (*authzed.Client, error) {
	options := []grpc.DialOption{grpcutil.WithInsecureBearerToken(cfg.PresharedKey),
		grpc.WithTransportCredentials(insecure.NewCredentials())}
	if !cfg.Insecure {
		tls, err := grpcutil.WithSystemCerts(grpcutil.VerifyCA)
		if err != nil {
			return nil, fmt.Errorf("spicedb tls: %w", err)
		}
		options = []grpc.DialOption{grpcutil.WithBearerToken(cfg.PresharedKey), tls}
	}
	client, err := authzed.NewClient(cfg.Endpoint, options...)
	if err != nil {
		return nil, fmt.Errorf("dial spicedb %s: %w", cfg.Endpoint, err)
	}
	return client, nil
}

func NewAuthorizer(permissions v1.PermissionsServiceClient, revisions RevisionReader, timeout time.Duration) *Authorizer {
	return &Authorizer{permissions: permissions, revisions: revisions, timeout: timeout}
}

func (a *Authorizer) Check(ctx context.Context, check application.AccessCheck) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	consistency := &v1.Consistency{Requirement: &v1.Consistency_MinimizeLatency{MinimizeLatency: true}}
	if check.Resource.Type == resourceOrder && a.revisions != nil {
		revision, ok, err := a.revisions.Revision(ctx, check.Resource.ID)
		if err != nil {
			return fmt.Errorf("%w: read authorization revision of order %s: %w", ErrUnavailable, check.Resource.ID, err)
		}
		if ok {
			consistency = &v1.Consistency{Requirement: &v1.Consistency_AtLeastAsFresh{
				AtLeastAsFresh: &v1.ZedToken{Token: string(revision)},
			}}
		}
	}
	resp, err := a.permissions.CheckPermission(ctx, &v1.CheckPermissionRequest{
		Consistency: consistency,
		Resource:    &v1.ObjectReference{ObjectType: string(check.Resource.Type), ObjectId: check.Resource.ID},
		Permission:  string(check.Permission),
		Subject:     subject(check.Subject),
	})
	if err != nil {
		return fmt.Errorf("%w: check %s on %s:%s: %w", ErrUnavailable, check.Permission, check.Resource.Type, check.Resource.ID, err)
	}
	if resp.GetPermissionship() != v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION {
		return fmt.Errorf("%w: %s %s on %s:%s", application.ErrPermissionDenied,
			check.Subject, check.Permission, check.Resource.Type, check.Resource.ID)
	}
	return nil
}

// GrantOrderRelationships writes order#customer and order#restaurant in one
// atomic request before the local commit and returns the ZedToken of the write;
// TOUCH is idempotent, so a retried Place Order repeats it safely.
func (a *Authorizer) GrantOrderRelationships(ctx context.Context, orderID order.ID, customerID order.CustomerID, restaurantID order.RestaurantID) (application.AuthorizationRevision, error) {
	resource := &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: string(orderID)}
	return a.write(ctx, fmt.Sprintf("touch order:%s#customer,restaurant", orderID),
		touch(resource, relationCustomer, objectSubject(subjectUser, string(customerID))),
		touch(resource, relationRestaurant, objectSubject(subjectRestaurant, string(restaurantID))),
	)
}

// AssignCourier writes order#courier and order#delivery of next and deletes
// those of previous that differ, in one atomic request: a reassignment never
// leaves the Order visible to both couriers or to none. TOUCH and DELETE are
// idempotent, so a redelivered event repeats the request safely.
func (a *Authorizer) AssignCourier(ctx context.Context, orderID order.ID, next recordcourierassignment.Assignment, previous *recordcourierassignment.Assignment) (application.AuthorizationRevision, error) {
	resource := &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: string(orderID)}
	updates := []*v1.RelationshipUpdate{
		touch(resource, relationCourier, objectSubject(subjectUser, next.CourierID)),
		touch(resource, relationDelivery, objectSubject(subjectDelivery, next.DeliveryID)),
	}
	if previous != nil && previous.CourierID != "" && previous.CourierID != next.CourierID {
		updates = append(updates, update(v1.RelationshipUpdate_OPERATION_DELETE, resource, relationCourier, objectSubject(subjectUser, previous.CourierID)))
	}
	if previous != nil && previous.DeliveryID != "" && previous.DeliveryID != next.DeliveryID {
		updates = append(updates, update(v1.RelationshipUpdate_OPERATION_DELETE, resource, relationDelivery, objectSubject(subjectDelivery, previous.DeliveryID)))
	}
	return a.write(ctx, fmt.Sprintf("assign courier of order:%s", orderID), updates...)
}

// ReleaseCourier deletes order#courier of the released courier and, when
// withDelivery, order#delivery in one request. DELETE of an absent
// relationship is a no-op, so a repeat is idempotent.
func (a *Authorizer) ReleaseCourier(ctx context.Context, orderID order.ID, released recordcourierassignment.Assignment, withDelivery bool) (application.AuthorizationRevision, error) {
	resource := &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: string(orderID)}
	var updates []*v1.RelationshipUpdate
	if released.CourierID != "" {
		updates = append(updates, update(v1.RelationshipUpdate_OPERATION_DELETE, resource, relationCourier, objectSubject(subjectUser, released.CourierID)))
	}
	if withDelivery && released.DeliveryID != "" {
		updates = append(updates, update(v1.RelationshipUpdate_OPERATION_DELETE, resource, relationDelivery, objectSubject(subjectDelivery, released.DeliveryID)))
	}
	if len(updates) == 0 {
		return "", nil
	}
	return a.write(ctx, fmt.Sprintf("release courier of order:%s", orderID), updates...)
}

// write applies updates in one WriteRelationships request, which SpiceDB
// commits atomically, and returns the ZedToken of the write.
func (a *Authorizer) write(ctx context.Context, operation string, updates ...*v1.RelationshipUpdate) (application.AuthorizationRevision, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	resp, err := a.permissions.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: updates})
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrUnavailable, operation, err)
	}
	return application.AuthorizationRevision(resp.GetWrittenAt().GetToken()), nil
}

func touch(resource *v1.ObjectReference, relation string, subject *v1.SubjectReference) *v1.RelationshipUpdate {
	return update(v1.RelationshipUpdate_OPERATION_TOUCH, resource, relation, subject)
}

func update(op v1.RelationshipUpdate_Operation, resource *v1.ObjectReference, relation string, subject *v1.SubjectReference) *v1.RelationshipUpdate {
	return &v1.RelationshipUpdate{Operation: op, Relationship: &v1.Relationship{Resource: resource, Relation: relation, Subject: subject}}
}

func objectSubject(objectType, id string) *v1.SubjectReference {
	return &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: objectType, ObjectId: id}}
}

func subject(s application.Subject) *v1.SubjectReference {
	return &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: string(s.Kind), ObjectId: s.ID}}
}
