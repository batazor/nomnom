package spicedb_test

import (
	"context"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

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

// customerCheckRequest is the CheckPermission request of customerCheck for an
// Order without a stored revision.
var customerCheckRequest = &v1.CheckPermissionRequest{
	Consistency: &v1.Consistency{Requirement: &v1.Consistency_MinimizeLatency{MinimizeLatency: true}},
	Resource:    &v1.ObjectReference{ObjectType: "order", ObjectId: "order-1"},
	Permission:  "view",
	Subject:     &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "customer-1"}},
}

func TestCheckMapsAccessCheckToSpiceDB(t *testing.T) {
	permissions := NewMockPermissions(t)
	permissions.EXPECT().CheckPermission(mock.Anything, mock.MatchedBy(func(r *v1.CheckPermissionRequest) bool {
		return proto.Equal(r, customerCheckRequest)
	})).Return(&v1.CheckPermissionResponse{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}, nil).Once()
	a := spicedb.NewAuthorizer(permissions, nil, time.Second)

	require.NoError(t, a.Check(context.Background(), customerCheck))
}

func TestCheckDeniesWithoutPermission(t *testing.T) {
	for _, p := range []v1.CheckPermissionResponse_Permissionship{
		v1.CheckPermissionResponse_PERMISSIONSHIP_NO_PERMISSION,
		v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
	} {
		permissions := NewMockPermissions(t)
		permissions.EXPECT().CheckPermission(mock.Anything, mock.MatchedBy(func(r *v1.CheckPermissionRequest) bool {
			return proto.Equal(r, customerCheckRequest)
		})).
			Return(&v1.CheckPermissionResponse{Permissionship: p}, nil).Once()
		a := spicedb.NewAuthorizer(permissions, nil, time.Second)
		require.ErrorIs(t, a.Check(context.Background(), customerCheck), application.ErrPermissionDenied)
	}
}

// AC-466: an unavailable SpiceDB fails closed.
func TestCheckFailsClosedOnError(t *testing.T) {
	permissions := NewMockPermissions(t)
	permissions.EXPECT().CheckPermission(mock.Anything, mock.MatchedBy(func(r *v1.CheckPermissionRequest) bool {
		return proto.Equal(r, customerCheckRequest)
	})).
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
	permissions := NewMockPermissions(t)
	permissions.EXPECT().CheckPermission(mock.Anything, mock.MatchedBy(func(r *v1.CheckPermissionRequest) bool {
		return proto.Equal(r, &v1.CheckPermissionRequest{
			Consistency: &v1.Consistency{Requirement: &v1.Consistency_AtLeastAsFresh{AtLeastAsFresh: &v1.ZedToken{Token: "zed-1"}}},
			Resource:    &v1.ObjectReference{ObjectType: "order", ObjectId: "order-1"},
			Permission:  "view",
			Subject:     &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "customer-1"}},
		})
	})).Return(&v1.CheckPermissionResponse{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}, nil).Once()
	a := spicedb.NewAuthorizer(permissions, revisions{"order-1": "zed-1"}, time.Second)

	require.NoError(t, a.Check(context.Background(), customerCheck))
}

// An unavailable SpiceDB fails the sync as a dependency error: the event is
// redelivered.
func TestSyncFailsAsUnavailable(t *testing.T) {
	permissions := NewMockPermissions(t)
	permissions.EXPECT().ReadRelationships(mock.Anything, mock.MatchedBy(func(r *v1.ReadRelationshipsRequest) bool {
		return proto.Equal(r, &v1.ReadRelationshipsRequest{
			Consistency:        &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
			RelationshipFilter: &v1.RelationshipFilter{ResourceType: "order", OptionalResourceId: "order-1"},
		})
	})).Return(nil, errors.New("unavailable")).Once()
	a := spicedb.NewAuthorizer(permissions, nil, time.Second)

	_, err := a.SyncOrderRelationships(context.Background(), "order-1", syncorderrelationships.Subjects{CustomerID: "customer-1", RestaurantID: "restaurant-1"})

	require.ErrorIs(t, err, spicedb.ErrUnavailable)
	require.ErrorIs(t, err, application.ErrDependencyUnavailable)
}

// The relationship sync and the freshness of checks by the stored ZedToken
// (AC-467, AC-516, AC-519, AC-535) against SpiceDB in a testcontainer with
// the repository bootstrap. Permission paths of the schema are asserted by
// `make authz-validate` (authorization/validation.yaml), not here.
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
	customerID := "customer-" + orderID

	sync := func(restaurantID, deliveryID string) {
		t.Helper()
		revision, err := a.SyncOrderRelationships(ctx, order.ID(orderID), syncorderrelationships.Subjects{
			CustomerID: order.CustomerID(customerID), RestaurantID: order.RestaurantID(restaurantID), DeliveryID: deliveryID,
		})
		require.NoError(t, err)
		stored[orderID] = revision
	}
	// relationships lists the stored relationships of the Order as
	// "relation@type:id", read fully consistently.
	relationships := func() []string {
		t.Helper()
		stream, err := client.ReadRelationships(ctx, &v1.ReadRelationshipsRequest{
			Consistency:        &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
			RelationshipFilter: &v1.RelationshipFilter{ResourceType: "order", OptionalResourceId: orderID},
		})
		require.NoError(t, err)
		var found []string
		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return found
			}
			require.NoError(t, err)
			r := resp.GetRelationship()
			found = append(found, r.GetRelation()+"@"+r.GetSubject().GetObject().GetObjectType()+":"+r.GetSubject().GetObject().GetObjectId())
		}
	}
	check := func(userID string) error {
		return a.Check(ctx, application.AccessCheck{
			Subject:    application.Subject{Kind: application.SubjectUser, ID: userID},
			Permission: "view",
			Resource:   application.Resource{Type: "order", ID: orderID},
		})
	}

	// AC-516, AC-467: the sync writes order#customer and order#restaurant, and
	// the check right after it reads at least as fresh as the stored revision.
	sync("restaurant-1", "")
	require.ElementsMatch(t, []string{"customer@user:" + customerID, "restaurant@restaurant:restaurant-1"}, relationships())
	require.NoError(t, check(customerID))

	// The sync replaces the restaurant and links the Delivery. delivery#courier
	// is written by delivery before the sync, so the stored revision covers it.
	deliveryA, deliveryB := "delivery-a-"+orderID, "delivery-b-"+orderID
	courierA, courierB := "courier-a-"+orderID, "courier-b-"+orderID
	touch(t, client, "delivery", deliveryA, "courier", "user", courierA)
	touch(t, client, "delivery", deliveryB, "courier", "user", courierB)
	sync("restaurant-2", deliveryA)
	require.ElementsMatch(t, []string{
		"customer@user:" + customerID, "restaurant@restaurant:restaurant-2", "delivery@delivery:" + deliveryA,
	}, relationships())

	// AC-519: a change of Delivery replaces the link; a repeated sync of the
	// same state changes nothing. The stored revision makes the change visible
	// to the next check at once.
	require.NoError(t, check(courierA))
	sync("restaurant-2", deliveryB)
	sync("restaurant-2", deliveryB)
	require.ElementsMatch(t, []string{
		"customer@user:" + customerID, "restaurant@restaurant:restaurant-2", "delivery@delivery:" + deliveryB,
	}, relationships())
	require.ErrorIs(t, check(courierA), application.ErrPermissionDenied)
	require.NoError(t, check(courierB))

	// AC-535: a cancelled Delivery unlinks the Order.
	sync("restaurant-2", "")
	require.ElementsMatch(t, []string{"customer@user:" + customerID, "restaurant@restaurant:restaurant-2"}, relationships())
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
