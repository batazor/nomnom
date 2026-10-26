package spicedb_test

import (
	"context"
	"testing"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/testfixture"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/permission"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/spicedb"
)

var endpoint string

func TestMain(m *testing.M) {
	s := testfixture.MustStartSpiceDB()
	endpoint = s.Endpoint
	defer s.Close()
	m.Run()
}

func manageMarkets(id string, kind application.SubjectKind) application.AccessCheck {
	return application.AccessCheck{
		Subject:    application.Subject{Kind: kind, ID: id, Service: "demo-seed"},
		Permission: permission.ManageMarkets,
		Resource:   application.Resource{Type: permission.ResourcePlatform, ID: permission.PlatformID},
	}
}

// The schema of authorization/ grants manage_markets to the platform operator only.
func TestOnlyTheOperatorManagesMarkets(t *testing.T) {
	ctx := context.Background()
	client, err := spicedb.Dial(config.SpiceDBConfig{Endpoint: endpoint, PresharedKey: testfixture.SpiceDBPresharedKey, Insecure: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: []*v1.RelationshipUpdate{{
		Operation: v1.RelationshipUpdate_OPERATION_TOUCH,
		Relationship: &v1.Relationship{
			Resource: &v1.ObjectReference{ObjectType: "platform", ObjectId: "nomnom"},
			Relation: "operator",
			Subject:  &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "test-operator"}},
		},
	}}})
	require.NoError(t, err)
	authorizer := spicedb.NewAuthorizer(client, 5*time.Second)

	assert.NoError(t, authorizer.Check(ctx, manageMarkets("test-operator", application.SubjectUser)))
	assert.ErrorIs(t, authorizer.Check(ctx, manageMarkets("test-stranger", application.SubjectUser)), application.ErrPermissionDenied)
	assert.ErrorIs(t, authorizer.Check(ctx, manageMarkets("bff", application.SubjectService)), application.ErrPermissionDenied)
}

func TestUnreachableSpiceDBIsUnavailable(t *testing.T) {
	client, err := spicedb.Dial(config.SpiceDBConfig{Endpoint: "127.0.0.1:1", PresharedKey: "key", Insecure: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	err = spicedb.NewAuthorizer(client, 500*time.Millisecond).Check(context.Background(), manageMarkets("test-operator", application.SubjectUser))
	assert.ErrorIs(t, err, application.ErrDependencyUnavailable)
}
