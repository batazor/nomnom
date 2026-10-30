// Package spicedb implements the authorization port of the topology slice on
// SpiceDB (ADR 18): the platform#manage_markets check of the commands.
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

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/authz"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/usecase"
)

// ErrUnavailable means SpiceDB did not answer; use cases fail closed on it.
var ErrUnavailable = fmt.Errorf("authorization service: %w", usecase.ErrDependencyUnavailable)

// Authorizer checks authz.AccessCheck as `<type>:<id>#<permission>` for
// `user:<id>` or `service:<id>`.
type Authorizer struct {
	permissions v1.PermissionsServiceClient
	timeout     time.Duration
}

var _ authz.Authorizer = (*Authorizer)(nil)

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
	// Client spans of every SpiceDB call; RPC metrics stay off: rpc.server.*
	// of the API already covers the latency of the commands.
	options = append(options, grpc.WithStatsHandler(otelgrpc.NewClientHandler(otelgrpc.WithMeterProvider(noop.NewMeterProvider()))))
	client, err := authzed.NewClient(cfg.Endpoint, options...)
	if err != nil {
		return nil, fmt.Errorf("dial spicedb %s: %w", cfg.Endpoint, err)
	}
	return client, nil
}

func NewAuthorizer(permissions v1.PermissionsServiceClient, timeout time.Duration) *Authorizer {
	return &Authorizer{permissions: permissions, timeout: timeout}
}

// Check asks SpiceDB with full consistency: grants of the operator change
// rarely, and a write must not pass on a stale revoked grant.
func (a *Authorizer) Check(ctx context.Context, check authz.AccessCheck) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	resp, err := a.permissions.CheckPermission(ctx, &v1.CheckPermissionRequest{
		Consistency: &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
		Resource:    &v1.ObjectReference{ObjectType: string(check.Resource.Type), ObjectId: check.Resource.ID},
		Permission:  string(check.Permission),
		Subject:     &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: string(check.Subject.Kind), ObjectId: check.Subject.ID}},
	})
	if err != nil {
		return fmt.Errorf("%w: check %s on %s:%s: %w", ErrUnavailable, check.Permission, check.Resource.Type, check.Resource.ID, err)
	}
	if resp.GetPermissionship() != v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION {
		return fmt.Errorf("%w: %s %s on %s:%s", authz.ErrPermissionDenied,
			check.Subject, check.Permission, check.Resource.Type, check.Resource.ID)
	}
	return nil
}
