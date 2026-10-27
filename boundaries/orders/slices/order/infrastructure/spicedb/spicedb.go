// Package spicedb implements the authorization ports of the order slice on
// SpiceDB (ADR 18): permission checks of the use cases and the sync of the
// order#customer, order#restaurant and order#delivery relationships with the
// Order in PostgreSQL. The schema has no order#courier:
// the courier sees the Order through delivery->act_as_courier.
// Schema: authorization/schema.zed.
package spicedb

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	"nomnom/orders/slices/order/applications/syncorderrelationships"
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

// Permissions is the part of the SpiceDB Permissions API the adapter calls.
type Permissions interface {
	CheckPermission(ctx context.Context, in *v1.CheckPermissionRequest, opts ...grpc.CallOption) (*v1.CheckPermissionResponse, error)
	ReadRelationships(ctx context.Context, in *v1.ReadRelationshipsRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[v1.ReadRelationshipsResponse], error)
	WriteRelationships(ctx context.Context, in *v1.WriteRelationshipsRequest, opts ...grpc.CallOption) (*v1.WriteRelationshipsResponse, error)
}

// Authorizer checks application.AccessCheck as `<type>:<id>#<permission>` for
// `user:<id>` or `service:<id>`. An Order with a stored authorization revision
// (the ZedToken of its last relationship sync) is checked at least as fresh as
// that write.
type Authorizer struct {
	permissions Permissions
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

func NewAuthorizer(permissions Permissions, revisions RevisionReader, timeout time.Duration) *Authorizer {
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

// SyncOrderRelationships makes the order relationships of orderID exactly
// subjects (ADR 18): it reads the stored ones fully consistently, TOUCHes the
// wanted and DELETEs the others in one atomic WriteRelationships, and returns
// its ZedToken. The result depends only on subjects, so a repeated or late
// sync is idempotent.
func (a *Authorizer) SyncOrderRelationships(ctx context.Context, orderID order.ID, subjects syncorderrelationships.Subjects) (application.AuthorizationRevision, error) {
	resource := &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: string(orderID)}
	wanted := map[string]*v1.SubjectReference{
		relationCustomer:   objectSubject(subjectUser, string(subjects.CustomerID)),
		relationRestaurant: objectSubject(subjectRestaurant, string(subjects.RestaurantID)),
	}
	if subjects.DeliveryID != "" {
		wanted[relationDelivery] = objectSubject(subjectDelivery, subjects.DeliveryID)
	}
	stored, err := a.relationshipsOf(ctx, resource)
	if err != nil {
		return "", err
	}
	updates := make([]*v1.RelationshipUpdate, 0, len(wanted)+len(stored))
	for relation, subject := range wanted {
		updates = append(updates, touch(resource, relation, subject))
	}
	for _, relationship := range stored {
		if want, ok := wanted[relationship.GetRelation()]; ok && sameSubject(want, relationship.GetSubject()) {
			continue
		}
		updates = append(updates, update(v1.RelationshipUpdate_OPERATION_DELETE, resource, relationship.GetRelation(), relationship.GetSubject()))
	}
	return a.write(ctx, fmt.Sprintf("sync relationships of order:%s", orderID), updates...)
}

// relationshipsOf reads every stored relationship of resource fully
// consistently: the sync must see the writes of the previous one.
func (a *Authorizer) relationshipsOf(ctx context.Context, resource *v1.ObjectReference) ([]*v1.Relationship, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	stream, err := a.permissions.ReadRelationships(ctx, &v1.ReadRelationshipsRequest{
		Consistency: &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
		RelationshipFilter: &v1.RelationshipFilter{
			ResourceType:       resource.GetObjectType(),
			OptionalResourceId: resource.GetObjectId(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: read relationships of %s:%s: %w", ErrUnavailable, resource.GetObjectType(), resource.GetObjectId(), err)
	}
	var relationships []*v1.Relationship
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return relationships, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: read relationships of %s:%s: %w", ErrUnavailable, resource.GetObjectType(), resource.GetObjectId(), err)
		}
		relationships = append(relationships, resp.GetRelationship())
	}
}

func sameSubject(a, b *v1.SubjectReference) bool {
	return a.GetObject().GetObjectType() == b.GetObject().GetObjectType() &&
		a.GetObject().GetObjectId() == b.GetObject().GetObjectId() &&
		a.GetOptionalRelation() == b.GetOptionalRelation()
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
