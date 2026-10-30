package repository_test

import (
	"context"
	"io/fs"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/infrastructure/migration"
	"nomnom/orders/pkg/config/database"
	"nomnom/orders/slices/order/infrastructure/repository"
)

// ADR 41: Orders placed before Markets belong to berlin. The migration sets it
// explicitly on existing orders and order_details rows and then makes the
// column required.
func TestMarketMigrationBackfillsExistingOrdersWithBerlin(t *testing.T) {
	ctx := context.Background()
	cfg := database.Config{DSN: emptyOrdersDatabase(t), MaxConns: 1}
	require.NoError(t, migration.Apply(ctx, cfg, migrationsBefore(t, "00008_market.up.sql")))

	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, total_amount, total_currency, state, placed_at, version)
		VALUES ('legacy-1', 'customer-1', 'restaurant-1', 10, 'EUR', 'placed', now(), 1)`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `INSERT INTO order_details (order_id, customer_id, placed_at, order_version, projection_version, details)
		VALUES ('legacy-1', 'customer-1', now(), 1, 1, '{}')`)
	require.NoError(t, err)

	require.NoError(t, migration.Apply(ctx, cfg, repository.Migrations))

	var orderMarket, detailsMarket string
	require.NoError(t, conn.QueryRow(ctx, `SELECT market_id FROM orders WHERE id = 'legacy-1'`).Scan(&orderMarket))
	require.NoError(t, conn.QueryRow(ctx, `SELECT market_id FROM order_details WHERE order_id = 'legacy-1'`).Scan(&detailsMarket))
	assert.Equal(t, "berlin", orderMarket)
	assert.Equal(t, "berlin", detailsMarket)

	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, total_amount, total_currency, state, placed_at, version, priority)
		VALUES ('new-1', 'customer-1', 'restaurant-1', 10, 'EUR', 'placed', now(), 1, 'STANDARD')`)
	require.ErrorContains(t, err, "market_id", "the Market has no default")
	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, market_id, total_amount, total_currency, state, placed_at, version, priority)
		VALUES ('new-2', 'customer-1', 'restaurant-1', 'Berlin', 10, 'EUR', 'placed', now(), 1, 'STANDARD')`)
	require.ErrorContains(t, err, "orders_market_id_format")
}

// FR-650: Orders placed before Premium Priority are STANDARD; afterwards the
// priority is required and a premium Order needs a positive fee.
func TestPremiumPriorityMigrationMakesExistingOrdersStandard(t *testing.T) {
	ctx := context.Background()
	cfg := database.Config{DSN: emptyOrdersDatabase(t), MaxConns: 1}
	require.NoError(t, migration.Apply(ctx, cfg, migrationsBefore(t, "00009_premium_priority.up.sql")))

	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, market_id, total_amount, total_currency, state, placed_at, version)
		VALUES ('legacy-1', 'customer-1', 'restaurant-1', 'berlin', 10, 'EUR', 'placed', now(), 1)`)
	require.NoError(t, err)

	require.NoError(t, migration.Apply(ctx, cfg, repository.Migrations))

	var priority string
	require.NoError(t, conn.QueryRow(ctx, `SELECT priority FROM orders WHERE id = 'legacy-1'`).Scan(&priority))
	assert.Equal(t, "STANDARD", priority)

	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, market_id, total_amount, total_currency, state, placed_at, version)
		VALUES ('new-1', 'customer-1', 'restaurant-1', 'berlin', 10, 'EUR', 'placed', now(), 1)`)
	require.ErrorContains(t, err, "priority", "the priority has no default")
	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, market_id, total_amount, total_currency, state, placed_at, version, priority)
		VALUES ('new-2', 'customer-1', 'restaurant-1', 'berlin', 10, 'EUR', 'placed', now(), 1, 'PREMIUM')`)
	require.ErrorContains(t, err, "orders_premium_fee_by_priority")
}

// Open Orders move to the furthest state their facts reached; closed Orders and
// Orders without progress facts stay.
func TestFulfillmentStatesMigrationDerivesStateFromFacts(t *testing.T) {
	ctx := context.Background()
	cfg := database.Config{DSN: emptyOrdersDatabase(t), MaxConns: 1}
	require.NoError(t, migration.Apply(ctx, cfg, migrationsBefore(t, "00011_fulfillment_states.up.sql")))

	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	tests := []struct {
		id, state string
		facts     []string
		want      string
	}{
		{"placed", "placed", nil, "placed"},
		{"paid", "placed", []string{"payment_authorized"}, "paid"},
		{"confirmed", "confirmed", []string{"payment_authorized", "restaurant_accepted", "courier_assigned"}, "confirmed"},
		{"preparing", "confirmed", []string{"restaurant_accepted", "preparation_started"}, "preparing"},
		{"ready", "confirmed", []string{"preparation_started", "ready_for_pickup"}, "ready"},
		{"picked-up", "confirmed", []string{"ready_for_pickup", "picked_up", "delivery_completed"}, "picked_up"},
		{"out-of-order", "placed", []string{"payment_authorized", "preparation_started"}, "preparing"},
		{"cancelled", "cancelled", []string{"preparation_started"}, "cancelled"},
		{"completed", "completed", []string{"picked_up"}, "completed"},
	}
	for _, tt := range tests {
		_, err := conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, market_id, total_amount, total_currency, state, placed_at, version, priority)
			VALUES ($1, 'customer-1', 'restaurant-1', 'berlin', 10, 'EUR', $2, now(), 1, 'STANDARD')`, tt.id, tt.state)
		require.NoError(t, err)
		for _, fact := range tt.facts {
			_, err := conn.Exec(ctx, `INSERT INTO order_facts (order_id, fact, occurred_at) VALUES ($1, $2, now())`, tt.id, fact)
			require.NoError(t, err)
		}
	}

	require.NoError(t, migration.Apply(ctx, cfg, migrationsBefore(t, "00012_settling_states.up.sql")))

	for _, tt := range tests {
		var state string
		require.NoError(t, conn.QueryRow(ctx, `SELECT state FROM orders WHERE id = $1`, tt.id).Scan(&state))
		assert.Equal(t, tt.want, state, tt.id)
	}
}

// An open Order with a known outcome moves to it; a closed Order stays.
func TestSettlingStatesMigrationDerivesStateFromFacts(t *testing.T) {
	ctx := context.Background()
	cfg := database.Config{DSN: emptyOrdersDatabase(t), MaxConns: 1}
	require.NoError(t, migration.Apply(ctx, cfg, migrationsBefore(t, "00012_settling_states.up.sql")))

	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	tests := []struct {
		id, state, fact, want string
	}{
		{"rejected", "paid", "restaurant_rejected", "rejected"},
		{"delivery-failed", "picked_up", "delivery_failed", "delivery_failed"},
		{"delivered", "picked_up", "delivery_completed", "delivered"},
		{"in-progress", "preparing", "preparation_started", "preparing"},
		{"cancelled", "cancelled", "restaurant_rejected", "cancelled"},
		{"completed", "completed", "delivery_completed", "completed"},
	}
	for _, tt := range tests {
		_, err := conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, market_id, total_amount, total_currency, state, placed_at, version, priority)
			VALUES ($1, 'customer-1', 'restaurant-1', 'berlin', 10, 'EUR', $2, now(), 1, 'STANDARD')`, tt.id, tt.state)
		require.NoError(t, err)
		_, err = conn.Exec(ctx, `INSERT INTO order_facts (order_id, fact, occurred_at) VALUES ($1, $2, now())`, tt.id, tt.fact)
		require.NoError(t, err)
	}

	require.NoError(t, migration.Apply(ctx, cfg, repository.Migrations))

	for _, tt := range tests {
		var state string
		require.NoError(t, conn.QueryRow(ctx, `SELECT state FROM orders WHERE id = $1`, tt.id).Scan(&state))
		assert.Equal(t, tt.want, state, tt.id)
	}
}

// The journal becomes the transition history: facts map to their triggers, the
// courier assignment stays in orders and a cancellation joins with its time.
func TestOrderTransitionsMigrationMapsFacts(t *testing.T) {
	ctx := context.Background()
	cfg := database.Config{DSN: emptyOrdersDatabase(t), MaxConns: 1}
	require.NoError(t, migration.Apply(ctx, cfg, migrationsBefore(t, "00013_order_transitions.up.sql")))

	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	_, err = conn.Exec(ctx, `INSERT INTO orders (id, customer_id, restaurant_id, market_id, total_amount, total_currency, state, placed_at, version, priority, cancelled_at, negative_outcome, cancellation_code)
		VALUES ('order-1', 'customer-1', 'restaurant-1', 'berlin', 10, 'EUR', 'cancelled', now(), 1, 'STANDARD', now(), 'failure', 'delivery_failed')`)
	require.NoError(t, err)
	mapped := map[string]string{
		"payment_authorized":  "authorize_payment",
		"restaurant_accepted": "confirm",
		"restaurant_rejected": "reject",
		"preparation_started": "start_preparation",
		"ready_for_pickup":    "mark_ready",
		"picked_up":           "pick_up",
		"delivery_completed":  "deliver",
		"delivery_failed":     "fail_delivery",
	}
	want := []string{"cancel"}
	for fact, trigger := range mapped {
		_, err := conn.Exec(ctx, `INSERT INTO order_facts (order_id, fact, occurred_at) VALUES ('order-1', $1, now())`, fact)
		require.NoError(t, err)
		want = append(want, trigger)
	}
	_, err = conn.Exec(ctx, `INSERT INTO order_facts (order_id, fact, occurred_at) VALUES ('order-1', 'courier_assigned', now())`)
	require.NoError(t, err)

	require.NoError(t, migration.Apply(ctx, cfg, migrationsBefore(t, "00014_drop_order_facts.up.sql")))

	rows, err := conn.Query(ctx, `SELECT trigger FROM order_transitions WHERE order_id = 'order-1'`)
	require.NoError(t, err)
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	assert.ElementsMatch(t, want, got)
	// The previous version of the code still reads the journal (ADR 42).
	var journal int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM order_facts`).Scan(&journal))
	assert.Equal(t, len(mapped)+1, journal)

	require.NoError(t, migration.Apply(ctx, cfg, repository.Migrations))

	var dropped *string
	require.NoError(t, conn.QueryRow(ctx, `SELECT to_regclass('order_facts')::text`).Scan(&dropped))
	assert.Nil(t, dropped)
	var history int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM order_transitions`).Scan(&history))
	assert.Equal(t, len(want), history)
}

// migrationsBefore is the order migration set without first and every later file.
func migrationsBefore(t *testing.T, first string) migration.Set {
	t.Helper()
	files := fstest.MapFS{}
	require.NoError(t, fs.WalkDir(repository.Migrations.Files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || strings.TrimPrefix(path, "migrations/") >= first {
			return err
		}
		data, err := fs.ReadFile(repository.Migrations.Files, path)
		files[path] = &fstest.MapFile{Data: data}
		return err
	}))
	return migration.Set{ID: repository.Migrations.ID, Files: files}
}

// emptyOrdersDatabase creates a database without schema in the package container.
func emptyOrdersDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, pg.DSN)
	require.NoError(t, err)
	defer func() { _ = admin.Close(ctx) }()
	name := "market_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	dsn, err := url.Parse(pg.DSN)
	require.NoError(t, err)
	dsn.Path = "/" + name
	return dsn.String()
}
