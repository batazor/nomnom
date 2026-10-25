package reconcile

import (
	"context"
	"errors"
	"io"
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

var customerRelation, restaurantRelation, deliveryRelation = Relations[0], Relations[1], Relations[2]

func (e *env) touch(orderID, customer string) { e.touchRelation(customerRelation, orderID, customer) }

func (e *env) touchRelation(relation Relation, orderID, subject string) {
	e.t.Helper()
	_, err := e.permissions.WriteRelationships(e.ctx, writeRequest(relation, v1.RelationshipUpdate_OPERATION_TOUCH, orderID, subject))
	require.NoError(e.t, err)
}

func (e *env) hasRelationship(orderID, customer string) bool {
	return e.hasRelation(customerRelation, orderID, customer)
}

func (e *env) hasRelation(relation Relation, orderID, subject string) bool {
	e.t.Helper()
	stream, err := e.permissions.ReadRelationships(e.ctx, &v1.ReadRelationshipsRequest{
		Consistency: &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
		RelationshipFilter: &v1.RelationshipFilter{
			ResourceType: resourceOrder, OptionalResourceId: orderID, OptionalRelation: string(relation.Name),
			OptionalSubjectFilter: &v1.SubjectFilter{SubjectType: relation.SubjectType, OptionalSubjectId: subject},
		},
	})
	require.NoError(e.t, err)
	_, err = stream.Recv()
	if errors.Is(err, io.EOF) {
		return false
	}
	require.NoError(e.t, err)
	return true
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

// AC-523: order#courier is not a reconciled relation: the schema has none, the
// courier sees the Order through delivery->act_as_courier, which the
// reconciler of delivery owns.
func TestRelationsAreCustomerRestaurantDelivery(t *testing.T) {
	names := make([]string, 0, len(Relations))
	for _, relation := range Relations {
		names = append(names, string(relation.Name))
	}
	assert.Equal(t, []string{"customer", "restaurant", "delivery"}, names)
}

// AC-524: the delivery relationship follows the stored Courier Assignment and
// restaurant follows the Order: a previous Delivery that survived a lost
// DELETE is stale and deleted after grace, the missing ones are restored.
func TestReconcileDeliveryAndRestaurantRelationships(t *testing.T) {
	e := newEnv(t)
	assigned := id("assigned")
	require.NoError(t, e.store.Add(e.ctx, ordertest.New(t, order.ID(assigned))))
	require.NoError(t, e.store.RememberCourierAssignment(e.ctx, order.ID(assigned), order.CourierAssignment{
		DeliveryID: "delivery-new", CourierID: "courier-1", AssignedAt: e.now,
	}, ""))
	e.touchRelation(deliveryRelation, assigned, "delivery-old")

	e.pass(0)
	assert.True(t, e.hasRelation(deliveryRelation, assigned, "delivery-new"), "missing delivery is restored")
	assert.True(t, e.hasRelation(restaurantRelation, assigned, "restaurant-1"), "missing restaurant is restored")
	assert.True(t, e.hasRelation(deliveryRelation, assigned, "delivery-old"), "first sighting only marks a candidate")

	e.pass(11 * time.Minute)
	assert.False(t, e.hasRelation(deliveryRelation, assigned, "delivery-old"), "stale delivery is deleted after grace")
	assert.True(t, e.hasRelation(deliveryRelation, assigned, "delivery-new"))
}

// After Record Courier Release the delivery column of a cancelled Delivery is
// NULL, so a surviving relationship is an orphan and is deleted after grace;
// an unassignment keeps the Delivery.
func TestReconcileReleasedDelivery(t *testing.T) {
	e := newEnv(t)
	unassigned, cancelled := id("unassigned"), id("cancelled")
	for _, orderID := range []string{unassigned, cancelled} {
		require.NoError(t, e.store.Add(e.ctx, ordertest.New(t, order.ID(orderID))))
		e.touchRelation(deliveryRelation, orderID, "delivery-1")
	}
	require.NoError(t, e.store.RememberCourierAssignment(e.ctx, order.ID(unassigned), order.CourierAssignment{
		DeliveryID: "delivery-1", AssignedAt: e.now,
	}, ""))
	require.NoError(t, e.store.RememberCourierAssignment(e.ctx, order.ID(cancelled), order.CourierAssignment{
		AssignedAt: e.now,
	}, ""))

	e.pass(0)
	assert.True(t, e.hasRelation(deliveryRelation, cancelled, "delivery-1"), "first sighting only marks a candidate")
	e.pass(11 * time.Minute)
	assert.True(t, e.hasRelation(deliveryRelation, unassigned, "delivery-1"), "the delivery stays after an unassignment")
	assert.False(t, e.hasRelation(deliveryRelation, cancelled, "delivery-1"), "a cancelled delivery is deleted")
}

// AC-667: an assignment of a new Delivery B wrote TOUCH B + DELETE A in
// SpiceDB and has not committed when the pass reads PostgreSQL. The page still
// names Delivery A as missing; the locked re-read waits for the commit, sees B
// and restores nothing, so A is never linked again.
func TestReconcileDoesNotResurrectReplacedDelivery(t *testing.T) {
	e := newEnv(t)
	reassigned := id("reassigned")
	require.NoError(t, e.store.Add(e.ctx, ordertest.New(t, order.ID(reassigned))))
	require.NoError(t, e.store.RememberCourierAssignment(e.ctx, order.ID(reassigned), order.CourierAssignment{
		DeliveryID: "delivery-a", CourierID: "courier-1", AssignedAt: e.now, DeliveryVersion: 2,
	}, ""))
	e.touchRelation(customerRelation, reassigned, "customer-1")
	e.touchRelation(restaurantRelation, reassigned, "restaurant-1")
	// The consumer of the assignment: row lock, SpiceDB write, commit later.
	locked, commit, committed := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		committed <- e.store.Do(e.ctx, func(ctx context.Context) error {
			if _, _, err := e.store.CourierAssignment(ctx, order.ID(reassigned)); err != nil {
				return err
			}
			e.touchRelation(deliveryRelation, reassigned, "delivery-b")
			close(locked)
			<-commit
			return e.store.RememberCourierAssignment(ctx, order.ID(reassigned), order.CourierAssignment{
				DeliveryID: "delivery-b", CourierID: "courier-2", AssignedAt: e.now, DeliveryVersion: 3,
			}, "")
		})
	}()
	<-locked

	passed := make(chan Stats, 1)
	go func() {
		stats, err := e.reconciler.Reconcile(e.ctx)
		assert.NoError(t, err)
		passed <- stats
	}()
	// The pass is blocked on the row lock of the in-flight assignment.
	require.Eventually(t, func() bool {
		var waiting int
		err := e.store.Pool().QueryRow(e.ctx, `SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&waiting)
		return err == nil && waiting > 0
	}, 10*time.Second, 20*time.Millisecond)
	close(commit)
	require.NoError(t, <-committed)
	stats := <-passed

	assert.GreaterOrEqual(t, stats.MissingSuperseded, 1)
	assert.False(t, e.hasRelation(deliveryRelation, reassigned, "delivery-a"), "the replaced Delivery is not restored")
	assert.True(t, e.hasRelation(deliveryRelation, reassigned, "delivery-b"))
}
