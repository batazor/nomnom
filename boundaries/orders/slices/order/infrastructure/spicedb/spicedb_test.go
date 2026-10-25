package spicedb_test

import (
	"context"
	"errors"
	"fmt"
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
	"nomnom/orders/slices/order/applications/recordcourierassignment"
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
// AC-516: customer and restaurant are written in one request.
func TestGrantOrderRelationshipsTouchesAndReadsItsWrite(t *testing.T) {
	fake := &fakePermissions{permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}
	stored := revisions{}
	a := spicedb.NewAuthorizer(fake, stored, time.Second)

	revision, err := a.GrantOrderRelationships(context.Background(), "order-1", "customer-1", "restaurant-1")
	require.NoError(t, err)
	assert.Equal(t, application.AuthorizationRevision("zed-1"), revision)
	stored["order-1"] = revision
	require.NoError(t, a.Check(context.Background(), customerCheck))

	require.Len(t, fake.writes, 1, "customer and restaurant are written in one request")
	updates := fake.writes[0].GetUpdates()
	require.Len(t, updates, 2)
	assert.Equal(t, v1.RelationshipUpdate_OPERATION_TOUCH, updates[0].GetOperation())
	assert.Equal(t, "customer", updates[0].GetRelationship().GetRelation())
	assert.Equal(t, "customer-1", updates[0].GetRelationship().GetSubject().GetObject().GetObjectId())
	assert.Equal(t, v1.RelationshipUpdate_OPERATION_TOUCH, updates[1].GetOperation())
	assert.Equal(t, "restaurant", updates[1].GetRelationship().GetRelation())
	assert.Equal(t, "restaurant", updates[1].GetRelationship().GetSubject().GetObject().GetObjectType())
	assert.Equal(t, "restaurant-1", updates[1].GetRelationship().GetSubject().GetObject().GetObjectId())
	assert.Equal(t, "zed-1", fake.checks[0].GetConsistency().GetAtLeastAsFresh().GetToken())
}

func relationships(updates []*v1.RelationshipUpdate) []string {
	var out []string
	for _, u := range updates {
		r := u.GetRelationship()
		out = append(out, fmt.Sprintf("%s %s#%s@%s:%s", u.GetOperation(), r.GetResource().GetObjectId(), r.GetRelation(),
			r.GetSubject().GetObject().GetObjectType(), r.GetSubject().GetObject().GetObjectId()))
	}
	return out
}

// AC-519: a reassignment touches the new courier and deletes the previous one
// in one request; an unchanged delivery is only touched.
func TestAssignCourierReplacesPreviousInOneRequest(t *testing.T) {
	fake := &fakePermissions{}
	a := spicedb.NewAuthorizer(fake, nil, time.Second)
	next := recordcourierassignment.Assignment{DeliveryID: "delivery-1", CourierID: "courier-2"}

	_, err := a.AssignCourier(context.Background(), "order-1", next, nil)
	require.NoError(t, err)
	revision, err := a.AssignCourier(context.Background(), "order-1", next, &recordcourierassignment.Assignment{DeliveryID: "delivery-1", CourierID: "courier-1"})
	require.NoError(t, err)

	assert.Equal(t, application.AuthorizationRevision("zed-1"), revision)
	assert.Equal(t, []string{
		"OPERATION_TOUCH order-1#courier@user:courier-2",
		"OPERATION_TOUCH order-1#delivery@delivery:delivery-1",
	}, relationships(fake.writes[0].GetUpdates()))
	assert.Equal(t, []string{
		"OPERATION_TOUCH order-1#courier@user:courier-2",
		"OPERATION_TOUCH order-1#delivery@delivery:delivery-1",
		"OPERATION_DELETE order-1#courier@user:courier-1",
	}, relationships(fake.writes[1].GetUpdates()))
}

func TestAssignCourierFailsAsUnavailable(t *testing.T) {
	a := spicedb.NewAuthorizer(&fakePermissions{err: errors.New("unavailable")}, nil, time.Second)

	_, err := a.AssignCourier(context.Background(), "order-1", recordcourierassignment.Assignment{DeliveryID: "d", CourierID: "c"}, nil)

	require.ErrorIs(t, err, spicedb.ErrUnavailable)
	require.ErrorIs(t, err, application.ErrDependencyUnavailable)
}

// AC-534, AC-535: an unassignment deletes only order#courier, a cancellation
// also order#delivery; a release without subjects writes nothing.
func TestReleaseCourierDeletesRelationships(t *testing.T) {
	fake := &fakePermissions{}
	a := spicedb.NewAuthorizer(fake, nil, time.Second)
	released := recordcourierassignment.Assignment{DeliveryID: "delivery-1", CourierID: "courier-1"}

	_, err := a.ReleaseCourier(context.Background(), "order-1", released, false)
	require.NoError(t, err)
	_, err = a.ReleaseCourier(context.Background(), "order-1", released, true)
	require.NoError(t, err)
	revision, err := a.ReleaseCourier(context.Background(), "order-1", recordcourierassignment.Assignment{}, true)
	require.NoError(t, err)

	assert.Equal(t, application.AuthorizationRevision(""), revision)
	require.Len(t, fake.writes, 2)
	assert.Equal(t, []string{"OPERATION_DELETE order-1#courier@user:courier-1"}, relationships(fake.writes[0].GetUpdates()))
	assert.Equal(t, []string{
		"OPERATION_DELETE order-1#courier@user:courier-1",
		"OPERATION_DELETE order-1#delivery@delivery:delivery-1",
	}, relationships(fake.writes[1].GetUpdates()))
}

// An assignment after a release has no previous courier to delete.
func TestAssignCourierAfterReleaseDeletesNothing(t *testing.T) {
	fake := &fakePermissions{}
	a := spicedb.NewAuthorizer(fake, nil, time.Second)

	_, err := a.AssignCourier(context.Background(), "order-1",
		recordcourierassignment.Assignment{DeliveryID: "delivery-2", CourierID: "courier-2"},
		&recordcourierassignment.Assignment{})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"OPERATION_TOUCH order-1#courier@user:courier-2",
		"OPERATION_TOUCH order-1#delivery@delivery:delivery-2",
	}, relationships(fake.writes[0].GetUpdates()))
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
	revision, err := a.GrantOrderRelationships(ctx, order.ID(orderID), order.CustomerID(customer.ID), "restaurant-1")
	require.NoError(t, err)
	stored[orderID] = revision
	require.NoError(t, check(customer, "view"))
	require.NoError(t, check(customer, "cancel"))
	require.ErrorIs(t, check(application.Subject{Kind: application.SubjectUser, ID: "stranger"}, "view"), application.ErrPermissionDenied)

	// AC-522: restaurant staff view through order#restaurant; the assigned
	// courier views until reassignment; nobody but the customer cancels.
	restaurantID := "restaurant-" + orderID
	owner := application.Subject{Kind: application.SubjectUser, ID: "owner-" + orderID}
	_, err = client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: []*v1.RelationshipUpdate{{
		Operation: v1.RelationshipUpdate_OPERATION_TOUCH,
		Relationship: &v1.Relationship{
			Resource: &v1.ObjectReference{ObjectType: "restaurant", ObjectId: restaurantID},
			Relation: "owner",
			Subject:  &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: owner.ID}},
		},
	}}})
	require.NoError(t, err)
	stored[orderID], err = a.GrantOrderRelationships(ctx, order.ID(orderID), order.CustomerID(customer.ID), order.RestaurantID(restaurantID))
	require.NoError(t, err)
	require.NoError(t, check(owner, "view"))
	require.ErrorIs(t, check(owner, "cancel"), application.ErrPermissionDenied)

	first := recordcourierassignment.Assignment{DeliveryID: "delivery-" + orderID, CourierID: "courier-1-" + orderID}
	second := recordcourierassignment.Assignment{DeliveryID: first.DeliveryID, CourierID: "courier-2-" + orderID}
	stored[orderID], err = a.AssignCourier(ctx, order.ID(orderID), first, nil)
	require.NoError(t, err)
	courier1 := application.Subject{Kind: application.SubjectUser, ID: first.CourierID}
	courier2 := application.Subject{Kind: application.SubjectUser, ID: second.CourierID}
	require.NoError(t, check(courier1, "view"))
	require.ErrorIs(t, check(courier1, "cancel"), application.ErrPermissionDenied)
	stored[orderID], err = a.AssignCourier(ctx, order.ID(orderID), second, &first)
	require.NoError(t, err)
	require.ErrorIs(t, check(courier1, "view"), application.ErrPermissionDenied, "the stored revision makes the revocation visible")
	require.NoError(t, check(courier2, "view"))

	// AC-534: the unassigned courier loses view at the stored revision.
	stored[orderID], err = a.ReleaseCourier(ctx, order.ID(orderID), second, false)
	require.NoError(t, err)
	require.ErrorIs(t, check(courier2, "view"), application.ErrPermissionDenied)

	worker := application.Subject{Kind: application.SubjectService, ID: "orders-worker"}
	require.NoError(t, a.Check(ctx, application.AccessCheck{
		Subject: worker, Permission: "manage_payments",
		Resource: application.Resource{Type: "platform", ID: "nomnom"},
	}))
}
