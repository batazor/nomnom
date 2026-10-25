package postgres_test

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

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

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
// apply the schema, started together they serialize on the advisory lock, and
// a later start finds nothing to change.
func TestStartupMigrationAppliesOnceAndThenChangesNothing(t *testing.T) {
	cfg := config.PostgresConfig{DSN: emptyDatabase(t), MaxConns: 2}
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = postgres.Migrate(ctx, cfg)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	version := func() int64 {
		var v int64
		require.NoError(t, conn.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version`).Scan(&v))
		return v
	}
	applied := version()
	var publication bool
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'orders_outbox')`).Scan(&publication))
	assert.True(t, publication, "the CDC publication exists before the process serves")

	require.NoError(t, postgres.Migrate(ctx, cfg))
	assert.Equal(t, applied, version(), "a migrated database is left as it is")
}
