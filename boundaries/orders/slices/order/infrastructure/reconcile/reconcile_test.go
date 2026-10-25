package reconcile

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/ordertest"
	"nomnom/orders/slices/order/infrastructure/postgres"
	"nomnom/orders/slices/order/infrastructure/spicedb"
)

type env struct {
	t           *testing.T
	ctx         context.Context
	store       *postgres.Store
	permissions v1.PermissionsServiceClient
	reconciler  *Reconciler
	now         time.Time
}

// newEnv needs `make spicedb-dev` (root) and `make postgres-dev` (orders).
func newEnv(t *testing.T) *env {
	t.Helper()
	endpoint, dsn := os.Getenv("SPICEDB_ENDPOINT"), os.Getenv("POSTGRES_DSN")
	if endpoint == "" || dsn == "" {
		t.Skip("SPICEDB_ENDPOINT and POSTGRES_DSN are not set")
	}
	ctx := context.Background()
	pg := config.PostgresConfig{DSN: dsn, MaxConns: 5}
	require.NoError(t, postgres.Migrate(ctx, pg))
	store, err := postgres.Open(ctx, pg)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	client, err := spicedb.Dial(config.SpiceDBConfig{Endpoint: endpoint, PresharedKey: "devkey", Insecure: true, Timeout: 2 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	e := &env{t: t, ctx: ctx, store: store, permissions: client, now: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	e.reconciler = New(client, store, 10*time.Minute, 2)
	e.reconciler.now = func() time.Time { return e.now }
	return e
}

func (e *env) touch(orderID, customer string) {
	e.t.Helper()
	_, err := e.permissions.WriteRelationships(e.ctx, writeRequest(v1.RelationshipUpdate_OPERATION_TOUCH, orderID, customer))
	require.NoError(e.t, err)
}

func (e *env) hasRelationship(orderID, customer string) bool {
	e.t.Helper()
	resp, err := e.permissions.CheckPermission(e.ctx, &v1.CheckPermissionRequest{
		Consistency: &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
		Resource:    &v1.ObjectReference{ObjectType: resourceOrder, ObjectId: orderID},
		Permission:  "view",
		Subject:     &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: subjectUser, ObjectId: customer}},
	})
	require.NoError(e.t, err)
	return resp.GetPermissionship() == v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION
}

func (e *env) pass(after time.Duration) Stats {
	e.t.Helper()
	e.now = e.now.Add(after)
	stats, err := e.reconciler.Reconcile(e.ctx)
	require.NoError(e.t, err)
	return stats
}

func id(prefix string) string { return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) }

// AC-473, AC-474, AC-475.
func TestReconcileOrderCustomerRelationships(t *testing.T) {
	e := newEnv(t)
	ghost, committing, missing, stale := id("ghost"), id("committing"), id("missing"), id("stale")

	// Placement wrote the relationship, then its transaction failed.
	e.touch(ghost, "customer-ghost")
	// Placement wrote the relationship; its Order commits between the passes.
	e.touch(committing, "customer-committing")
	// SpiceDB lost the relationship of a committed Order.
	require.NoError(t, e.store.Add(e.ctx, ordertest.New(t, order.ID(missing), ordertest.WithCustomer("customer-missing"))))
	// The relationship names another customer than the Order.
	require.NoError(t, e.store.Add(e.ctx, ordertest.New(t, order.ID(stale), ordertest.WithCustomer("customer-stale"))))
	e.touch(stale, "customer-wrong")

	first := e.pass(0)
	assert.GreaterOrEqual(t, first.OrphansPending, 3)
	assert.GreaterOrEqual(t, first.MissingRepaired, 2)
	assert.True(t, e.hasRelationship(ghost, "customer-ghost"), "first sighting only marks a candidate")
	assert.True(t, e.hasRelationship(missing, "customer-missing"), "missing relationship is restored")
	assert.True(t, e.hasRelationship(stale, "customer-stale"))
	revision, found, err := e.store.Revision(e.ctx, missing)
	require.NoError(t, err)
	assert.True(t, found)
	assert.NotEmpty(t, revision)

	require.NoError(t, e.store.Add(e.ctx, ordertest.New(t, order.ID(committing), ordertest.WithCustomer("customer-committing"))))
	e.pass(5 * time.Minute)
	assert.True(t, e.hasRelationship(ghost, "customer-ghost"), "inside grace the orphan stays")

	third := e.pass(6 * time.Minute)
	assert.GreaterOrEqual(t, third.OrphansDeleted, 2)
	assert.False(t, e.hasRelationship(ghost, "customer-ghost"))
	assert.False(t, e.hasRelationship(stale, "customer-wrong"))
	assert.True(t, e.hasRelationship(stale, "customer-stale"))
	assert.True(t, e.hasRelationship(committing, "customer-committing"), "a committed Order keeps its relationship")
}
