package schema_test

import (
	"context"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/di/schema"
	"nomnom/orders/pkg/config/database"
	"nomnom/orders/pkg/testfixture"
)

var pg *testfixture.Postgres

func TestMain(m *testing.M) {
	pg = testfixture.MustStartPostgres()
	defer pg.Close()
	m.Run()
}

// emptyDatabase creates a database without schema in the package container.
func emptyDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, pg.DSN)
	require.NoError(t, err)
	defer func() { _ = admin.Close(ctx) }()
	name := "startup_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	dsn, err := url.Parse(pg.DSN)
	require.NoError(t, err)
	dsn.Path = "/" + name
	return dsn.String()
}

// Every process migrates at startup: on an empty database the first starts
// apply every set, started together they serialize on the advisory lock, and
// a later start finds nothing to change.
func TestMigrateAppliesEverySetOnceAndThenChangesNothing(t *testing.T) {
	cfg := database.Config{DSN: emptyDatabase(t), MaxConns: 2}
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = schema.Migrate(ctx, cfg)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	versions := func() map[string]int64 {
		applied := map[string]int64{}
		for _, table := range []string{"schema_migrations_orders", "schema_migrations_simulator"} {
			var (
				v     int64
				dirty bool
			)
			require.NoError(t, conn.QueryRow(ctx, `SELECT version, dirty FROM `+table).Scan(&v, &dirty))
			require.False(t, dirty, "the last migration of %s completed", table)
			applied[table] = v
		}
		return applied
	}
	applied := versions()
	for _, table := range []string{"orders_outbox", "orders", "inbox", "idempotency_keys", "simulator_state"} {
		var exists bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		assert.True(t, exists, table)
	}
	var publication bool
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'orders_outbox')`).Scan(&publication))
	assert.True(t, publication, "the CDC publication exists before the process serves")

	require.NoError(t, schema.Migrate(ctx, cfg))
	assert.Equal(t, applied, versions(), "a migrated database is left as it is")
}
