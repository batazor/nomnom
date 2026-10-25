// Package spicedb implements the authorization ports of the order slice on
// SpiceDB (ADR 18): permission checks of the use cases and the creation-time
// order#customer relationship. Schema: authorization/schema.zed.
package spicedb

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	authzed "github.com/authzed/authzed-go/v1"
	"github.com/authzed/grpcutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/domains/order"
)

// ErrUnavailable means SpiceDB did not answer; use cases fail closed on it.
var ErrUnavailable = errors.New("authorization service unavailable")

const (
	resourceOrder    = "order"
	relationCustomer = "customer"
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

// GrantOrderCustomer writes order#customer before the local commit and returns
// the ZedToken of the write; TOUCH is idempotent, so a retried Place Order
// repeats it safely.
func (a *Authorizer) GrantOrderCustomer(ctx context.Context, orderID order.ID, customerID order.CustomerID) (application.AuthorizationRevision, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	resp, err := a.permissions.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{
		Updates: []*v1.RelationshipUpdate{{
			Operation: v1.RelationshipUpdate_OPERATION_TOUCH,
			Relationship: &v1.Relationship{
				Resource: &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: string(orderID)},
				Relation: relationCustomer,
				Subject:  subject(application.Subject{Kind: application.SubjectUser, ID: string(customerID)}),
			},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("%w: touch order:%s#customer: %w", ErrUnavailable, orderID, err)
	}
	return application.AuthorizationRevision(resp.GetWrittenAt().GetToken()), nil
}

func subject(s application.Subject) *v1.SubjectReference {
	return &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: string(s.Kind), ObjectId: s.ID}}
}
