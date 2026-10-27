package spicedb_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/testfixture"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/spicedb"
)

var customerCheck = application.AccessCheck{
	Subject:    application.Subject{Kind: application.SubjectUser, ID: "customer-1"},
	Permission: "view",
	Resource:   application.Resource{Type: "order", ID: "order-1"},
}

func TestCheckMapsAccessCheckToSpiceDB(t *testing.T) {
	permissions := NewMockPermissionsServiceClient(t)
	permissions.EXPECT().CheckPermission(mock.Anything, mock.MatchedBy(func(r *v1.CheckPermissionRequest) bool {
		return r.GetResource().GetObjectType() == "order" &&
			r.GetResource().GetObjectId() == "order-1" &&
			r.GetPermission() == "view" &&
			r.GetSubject().GetObject().GetObjectType() == "user" &&
			r.GetConsistency().GetMinimizeLatency()
	})).Return(&v1.CheckPermissionResponse{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}, nil).Once()
	a := spicedb.NewAuthorizer(permissions, nil, time.Second)

	require.NoError(t, a.Check(context.Background(), customerCheck))
}

func TestCheckDeniesWithoutPermission(t *testing.T) {
	for _, p := range []v1.CheckPermissionResponse_Permissionship{
		v1.CheckPermissionResponse_PERMISSIONSHIP_NO_PERMISSION,
		v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
	} {
		permissions := NewMockPermissionsServiceClient(t)
		permissions.EXPECT().CheckPermission(mock.Anything, mock.Anything).
			Return(&v1.CheckPermissionResponse{Permissionship: p}, nil).Once()
		a := spicedb.NewAuthorizer(permissions, nil, time.Second)
		require.ErrorIs(t, a.Check(context.Background(), customerCheck), application.ErrPermissionDenied)
	}
}

// AC-466: an unavailable SpiceDB fails closed.
func TestCheckFailsClosedOnError(t *testing.T) {
	permissions := NewMockPermissionsServiceClient(t)
	permissions.EXPECT().CheckPermission(mock.Anything, mock.Anything).
		Return(&v1.CheckPermissionResponse{}, errors.New("unavailable")).Once()
	a := spicedb.NewAuthorizer(permissions, nil, time.Second)

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
func TestCheckReadsAtLeastAsFreshAsTheStoredRevision(t *testing.T) {
	permissions := NewMockPermissionsServiceClient(t)
	permissions.EXPECT().CheckPermission(mock.Anything, mock.MatchedBy(func(r *v1.CheckPermissionRequest) bool {
		return r.GetConsistency().GetAtLeastAsFresh().GetToken() == "zed-1"
	})).Return(&v1.CheckPermissionResponse{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}, nil).Once()
	a := spicedb.NewAuthorizer(permissions, revisions{"order-1": "zed-1"}, time.Second)

	require.NoError(t, a.Check(context.Background(), customerCheck))
}

// An unavailable SpiceDB fails the sync as a dependency error: the event is
// redelivered.
func TestSyncFailsAsUnavailable(t *testing.T) {
	permissions := NewMockPermissionsServiceClient(t)
	permissions.EXPECT().ReadRelationships(mock.Anything, mock.Anything).Return(nil, errors.New("unavailable")).Once()
	a := spicedb.NewAuthorizer(permissions, nil, time.Second)

	_, err := a.SyncOrderRelationships(context.Background(), "order-1", syncorderrelationships.Subjects{CustomerID: "customer-1", RestaurantID: "restaurant-1"})

	require.ErrorIs(t, err, spicedb.ErrUnavailable)
	require.ErrorIs(t, err, application.ErrDependencyUnavailable)
}

// AC-465, AC-467, AC-518, AC-519, AC-522, AC-534, AC-535 against SpiceDB in a
// testcontainer with the repository bootstrap.
func TestAgainstSpiceDB(t *testing.T) {
	server, err := testfixture.StartSpiceDB(context.Background())
	require.NoError(t, err)
	t.Cleanup(server.Close)
	client, err := spicedb.Dial(config.SpiceDBConfig{Endpoint: server.Endpoint, PresharedKey: testfixture.SpiceDBPresharedKey, Insecure: true, Timeout: 2 * time.Second})
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

	sync := func(restaurantID, deliveryID string) {
		t.Helper()
		revision, err := a.SyncOrderRelationships(ctx, order.ID(orderID), syncorderrelationships.Subjects{
			CustomerID: order.CustomerID(customer.ID), RestaurantID: order.RestaurantID(restaurantID), DeliveryID: deliveryID,
		})
		require.NoError(t, err)
		stored[orderID] = revision
	}

	// AC-516: the sync writes order#customer and order#restaurant.
	require.ErrorIs(t, check(customer, "view"), application.ErrPermissionDenied)
	sync("restaurant-1", "")
	require.NoError(t, check(customer, "view"))
	require.NoError(t, check(customer, "cancel"))
	require.ErrorIs(t, check(application.Subject{Kind: application.SubjectUser, ID: "stranger"}, "view"), application.ErrPermissionDenied)

	// AC-522: restaurant staff view through order#restaurant; the courier
	// views while delivery#courier names them; nobody but the customer cancels.
	// The sync replaces the restaurant of the previous state.
	restaurantID := "restaurant-" + orderID
	owner := application.Subject{Kind: application.SubjectUser, ID: "owner-" + orderID}
	touch(t, client, "restaurant", restaurantID, "owner", "user", owner.ID)
	sync(restaurantID, "")
	require.NoError(t, check(owner, "view"))
	require.ErrorIs(t, check(owner, "cancel"), application.ErrPermissionDenied)

	// AC-518: order#delivery links the Order; the courier of the Delivery
	// (delivery#courier, written by delivery) views it, another courier does not.
	deliveryA, deliveryB := "delivery-a-"+orderID, "delivery-b-"+orderID
	courier1 := application.Subject{Kind: application.SubjectUser, ID: "courier-1-" + orderID}
	courier2 := application.Subject{Kind: application.SubjectUser, ID: "courier-2-" + orderID}
	touch(t, client, "delivery", deliveryA, "courier", "user", courier1.ID)
	touch(t, client, "delivery", deliveryB, "courier", "user", courier2.ID)
	require.ErrorIs(t, check(courier1, "view"), application.ErrPermissionDenied, "no order#delivery yet")
	sync(restaurantID, deliveryA)
	require.NoError(t, check(courier1, "view"))
	require.ErrorIs(t, check(courier1, "cancel"), application.ErrPermissionDenied)
	require.ErrorIs(t, check(courier2, "view"), application.ErrPermissionDenied, "a courier without delivery#courier of this Delivery")

	// AC-519: a change of Delivery replaces the link; a repeated sync of the
	// same state changes nothing.
	sync(restaurantID, deliveryB)
	sync(restaurantID, deliveryB)
	require.ErrorIs(t, check(courier1, "view"), application.ErrPermissionDenied, "the stored revision makes the change visible")
	require.NoError(t, check(courier2, "view"))

	// AC-535: a cancelled Delivery unlinks the Order.
	sync(restaurantID, "")
	require.ErrorIs(t, check(courier2, "view"), application.ErrPermissionDenied)
	require.NoError(t, check(customer, "view"))

	worker := application.Subject{Kind: application.SubjectService, ID: "orders-worker"}
	require.NoError(t, a.Check(ctx, application.AccessCheck{
		Subject: worker, Permission: "manage_payments",
		Resource: application.Resource{Type: "platform", ID: "nomnom"},
	}))
}

// touch writes resourceType:resourceID#relation@subjectType:subjectID as the
// owning context would.
func touch(t *testing.T, client v1.PermissionsServiceClient, resourceType, resourceID, relation, subjectType, subjectID string) {
	t.Helper()
	_, err := client.WriteRelationships(context.Background(), &v1.WriteRelationshipsRequest{Updates: []*v1.RelationshipUpdate{{
		Operation: v1.RelationshipUpdate_OPERATION_TOUCH,
		Relationship: &v1.Relationship{
			Resource: &v1.ObjectReference{ObjectType: resourceType, ObjectId: resourceID},
			Relation: relation,
			Subject:  &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: subjectType, ObjectId: subjectID}},
		},
	}}})
	require.NoError(t, err)
}
