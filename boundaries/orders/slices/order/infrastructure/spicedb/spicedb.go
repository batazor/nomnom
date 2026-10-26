// Package spicedb implements the authorization ports of the order slice on
// SpiceDB (ADR 18): permission checks of the use cases, the creation-time
// order#customer and order#restaurant relationships and the order#delivery
// relationship of the Courier Assignment. The schema has no order#courier:
// the courier sees the Order through delivery->act_as_courier.
// Schema: authorization/schema.zed.
package spicedb

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	authzed "github.com/authzed/authzed-go/v1"
	"github.com/authzed/grpcutil"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/domains/order"
)

// ErrUnavailable means SpiceDB did not answer; use cases fail closed on it.
var ErrUnavailable = fmt.Errorf("authorization service: %w", application.ErrDependencyUnavailable)

const (
	resourceOrder      = "order"
	relationCustomer   = "customer"
	relationRestaurant = "restaurant"
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
	checks      checkMetrics
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
	// Client spans of every SpiceDB call (trace context goes in metadata; the
	// preshared key header is not recorded). RPC metrics stay off: SpiceDB
	// latency and outcome are the authz metrics of the Authorization port, and
	// rpc.client.* covers calls through the internal gateway only.
	options = append(options, grpc.WithStatsHandler(otelgrpc.NewClientHandler(otelgrpc.WithMeterProvider(noop.NewMeterProvider()))))
	client, err := authzed.NewClient(cfg.Endpoint, options...)
	if err != nil {
		return nil, fmt.Errorf("dial spicedb %s: %w", cfg.Endpoint, err)
	}
	return client, nil
}

func NewAuthorizer(permissions v1.PermissionsServiceClient, revisions RevisionReader, timeout time.Duration) *Authorizer {
	return &Authorizer{permissions: permissions, revisions: revisions, timeout: timeout, checks: newCheckMetrics()}
}

// Check answers the AccessCheck and records nomnom.authz.checks and
// nomnom.authz.check.duration of it (checks.go).
func (a *Authorizer) Check(ctx context.Context, check application.AccessCheck) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	started := time.Now()
	err := a.check(ctx, check)
	a.checks.record(ctx, check, checkResult(ctx, err), time.Since(started))
	return err
}

func (a *Authorizer) check(ctx context.Context, check application.AccessCheck) error {

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

// LinkDelivery writes order#delivery of deliveryID and deletes that of
// displacedDeliveryID (when set) in one atomic request: the Order never links
// both Deliveries or none. The courier sees the Order through
// delivery->act_as_courier, which delivery owns. TOUCH and DELETE are
// idempotent, so a redelivered event repeats the request safely.
func (a *Authorizer) LinkDelivery(ctx context.Context, orderID order.ID, deliveryID, displacedDeliveryID string) (application.AuthorizationRevision, error) {
	resource := &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: string(orderID)}
	updates := []*v1.RelationshipUpdate{touch(resource, relationDelivery, objectSubject(subjectDelivery, deliveryID))}
	if displacedDeliveryID != "" && displacedDeliveryID != deliveryID {
		updates = append(updates, update(v1.RelationshipUpdate_OPERATION_DELETE, resource, relationDelivery, objectSubject(subjectDelivery, displacedDeliveryID)))
	}
	return a.write(ctx, fmt.Sprintf("link delivery of order:%s", orderID), updates...)
}

// UnlinkDelivery deletes order#delivery of a cancelled Delivery. DELETE of an
// absent relationship is a no-op, so a repeat is idempotent.
func (a *Authorizer) UnlinkDelivery(ctx context.Context, orderID order.ID, deliveryID string) (application.AuthorizationRevision, error) {
	resource := &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: string(orderID)}
	return a.write(ctx, fmt.Sprintf("unlink delivery of order:%s", orderID),
		update(v1.RelationshipUpdate_OPERATION_DELETE, resource, relationDelivery, objectSubject(subjectDelivery, deliveryID)))
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
