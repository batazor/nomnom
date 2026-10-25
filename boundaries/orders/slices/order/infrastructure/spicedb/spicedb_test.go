package spicedb_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/spicedb"
)

type fakePermissions struct {
	v1.PermissionsServiceClient
	checks         []*v1.CheckPermissionRequest
	writes         []*v1.WriteRelationshipsRequest
	permissionship v1.CheckPermissionResponse_Permissionship
	err            error
}

func (f *fakePermissions) CheckPermission(_ context.Context, r *v1.CheckPermissionRequest, _ ...grpc.CallOption) (*v1.CheckPermissionResponse, error) {
	f.checks = append(f.checks, r)
	return &v1.CheckPermissionResponse{Permissionship: f.permissionship}, f.err
}

func (f *fakePermissions) WriteRelationships(_ context.Context, r *v1.WriteRelationshipsRequest, _ ...grpc.CallOption) (*v1.WriteRelationshipsResponse, error) {
	f.writes = append(f.writes, r)
	return &v1.WriteRelationshipsResponse{WrittenAt: &v1.ZedToken{Token: "zed-1"}}, f.err
}

var customerCheck = application.AccessCheck{
	Subject:    application.Subject{Kind: application.SubjectUser, ID: "customer-1"},
	Permission: "view",
	Resource:   application.Resource{Type: "order", ID: "order-1"},
}

func TestCheckMapsAccessCheckToSpiceDB(t *testing.T) {
	fake := &fakePermissions{permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}
	a := spicedb.NewAuthorizer(fake, nil, time.Second)

	require.NoError(t, a.Check(context.Background(), customerCheck))

	require.Len(t, fake.checks, 1)
	assert.Equal(t, "order", fake.checks[0].GetResource().GetObjectType())
	assert.Equal(t, "order-1", fake.checks[0].GetResource().GetObjectId())
	assert.Equal(t, "view", fake.checks[0].GetPermission())
	assert.Equal(t, "user", fake.checks[0].GetSubject().GetObject().GetObjectType())
	assert.True(t, fake.checks[0].GetConsistency().GetMinimizeLatency())
}

func TestCheckDeniesWithoutPermission(t *testing.T) {
	for _, p := range []v1.CheckPermissionResponse_Permissionship{
		v1.CheckPermissionResponse_PERMISSIONSHIP_NO_PERMISSION,
		v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
	} {
		a := spicedb.NewAuthorizer(&fakePermissions{permissionship: p}, nil, time.Second)
		require.ErrorIs(t, a.Check(context.Background(), customerCheck), application.ErrPermissionDenied)
	}
}

// AC-466: an unavailable SpiceDB fails closed.
func TestCheckFailsClosedOnError(t *testing.T) {
	a := spicedb.NewAuthorizer(&fakePermissions{err: errors.New("unavailable")}, nil, time.Second)

	err := a.Check(context.Background(), customerCheck)

	require.ErrorIs(t, err, spicedb.ErrUnavailable)
	require.ErrorIs(t, application.FailClosed(err), application.ErrPermissionDenied)
}

type revisions map[string]application.AuthorizationRevision

func (r revisions) Revision(_ context.Context, id string) (application.AuthorizationRevision, bool, error) {
	revision, ok := r[id]
	return revision, ok, nil
}

// AC-472: the stored revision of an Order makes the check at least as fresh.
func TestGrantOrderCustomerTouchesAndReadsItsWrite(t *testing.T) {
	fake := &fakePermissions{permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}
	stored := revisions{}
	a := spicedb.NewAuthorizer(fake, stored, time.Second)

	revision, err := a.GrantOrderCustomer(context.Background(), "order-1", "customer-1")
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("zed-1"), revision)
	stored["order-1"] = revision
	require.NoError(t, a.Check(context.Background(), customerCheck))

	update := fake.writes[0].GetUpdates()[0]
	assert.Equal(t, v1.RelationshipUpdate_OPERATION_TOUCH, update.GetOperation())
	assert.Equal(t, "customer", update.GetRelationship().GetRelation())
	assert.Equal(t, "customer-1", update.GetRelationship().GetSubject().GetObject().GetObjectId())
	assert.Equal(t, "zed-1", fake.checks[0].GetConsistency().GetAtLeastAsFresh().GetToken())
}

// AC-465, AC-467 against the SpiceDB of `make spicedb-dev`.
func TestAgainstSpiceDB(t *testing.T) {
	endpoint := os.Getenv("SPICEDB_ENDPOINT")
	if endpoint == "" {
		t.Skip("SPICEDB_ENDPOINT is not set")
	}
	client, err := spicedb.Dial(config.SpiceDBConfig{Endpoint: endpoint, PresharedKey: "devkey", Insecure: true, Timeout: 2 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	stored := revisions{}
	a := spicedb.NewAuthorizer(client, stored, 2*time.Second)
	ctx := context.Background()
	orderID := "order-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	check := func(subject application.Subject, permission application.Permission) error {
		return a.Check(ctx, application.AccessCheck{
			Subject: subject, Permission: permission,
			Resource: application.Resource{Type: "order", ID: orderID},
		})
	}
	customer := application.Subject{Kind: application.SubjectUser, ID: "customer-" + orderID}

	require.ErrorIs(t, check(customer, "view"), application.ErrPermissionDenied)
	revision, err := a.GrantOrderCustomer(ctx, order.ID(orderID), order.CustomerID(customer.ID))
	require.NoError(t, err)
	stored[orderID] = revision
	require.NoError(t, check(customer, "view"))
	require.NoError(t, check(customer, "cancel"))
	require.ErrorIs(t, check(application.Subject{Kind: application.SubjectUser, ID: "stranger"}, "view"), application.ErrPermissionDenied)

	worker := application.Subject{Kind: application.SubjectService, ID: "orders-worker"}
	require.NoError(t, a.Check(ctx, application.AccessCheck{
		Subject: worker, Permission: "manage_payments",
		Resource: application.Resource{Type: "platform", ID: "nomnom"},
	}))
}
