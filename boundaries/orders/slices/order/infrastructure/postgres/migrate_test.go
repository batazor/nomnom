package postgres_test

import (
	"context"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
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
		var (
			v     int64
			dirty bool
		)
		require.NoError(t, conn.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&v, &dirty))
		require.False(t, dirty, "the last migration completed")
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

func migrationFiles(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, sql := range files {
		fsys["migrations/"+name] = &fstest.MapFile{Data: []byte(sql)}
	}
	return fsys
}

func schemaVersion(t *testing.T, conn *pgx.Conn) (int64, bool) {
	t.Helper()
	var (
		version int64
		dirty   bool
	)
	require.NoError(t, conn.QueryRow(context.Background(), `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty))
	return version, dirty
}

func tableExists(t *testing.T, conn *pgx.Conn, name string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, conn.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists))
	return exists
}

// A failed migration file leaves no changes (one implicit transaction) and a
// dirty version; the next start returns it to the previous version and
// applies the file again instead of refusing to start.
func TestStartupMigrationRetriesAnIncompleteMigration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first string
	}{
		{name: "first migration", first: "1_a.up.sql"},
		{name: "later migration", first: "2_a.up.sql"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cfg := config.PostgresConfig{DSN: emptyDatabase(t)}
			files := map[string]string{"1_base.up.sql": `CREATE TABLE base (id int);`}
			if tc.first == "1_a.up.sql" {
				files = map[string]string{}
			}
			files[tc.first] = `CREATE TABLE a (id int); SELECT 1 / 0;`
			require.Error(t, postgres.MigrateFiles(ctx, cfg, migrationFiles(files)))

			conn, err := pgx.Connect(ctx, cfg.DSN)
			require.NoError(t, err)
			defer func() { _ = conn.Close(ctx) }()
			_, dirty := schemaVersion(t, conn)
			require.True(t, dirty)
			assert.False(t, tableExists(t, conn, "a"), "the failed file left no changes")

			files[tc.first] = `CREATE TABLE a (id int);`
			require.NoError(t, postgres.MigrateFiles(ctx, cfg, migrationFiles(files)))
			version, dirty := schemaVersion(t, conn)
			assert.False(t, dirty)
			assert.Equal(t, int64(len(files)), version)
			assert.True(t, tableExists(t, conn, "a"))
		})
	}
}

// A migration waiting for a lock held by live traffic fails within
// lock_timeout instead of stalling the table, and succeeds on the next start.
func TestStartupMigrationGivesUpOnALockedTable(t *testing.T) {
	ctx := context.Background()
	cfg := config.PostgresConfig{DSN: emptyDatabase(t)}
	files := map[string]string{"1_a.up.sql": `CREATE TABLE a (id int);`}
	require.NoError(t, postgres.MigrateFiles(ctx, cfg, migrationFiles(files)))

	traffic, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = traffic.Close(ctx) }()
	tx, err := traffic.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT * FROM a`)
	require.NoError(t, err)

	files["2_column.up.sql"] = `ALTER TABLE a ADD COLUMN name text;`
	started := time.Now()
	err = postgres.MigrateFiles(ctx, cfg, migrationFiles(files))
	// golang-migrate wraps the driver error without Unwrap.
	require.ErrorContains(t, err, "SQLSTATE 55P03", "lock_not_available")
	assert.Less(t, time.Since(started), 30*time.Second)

	require.NoError(t, tx.Rollback(ctx))
	require.NoError(t, postgres.MigrateFiles(ctx, cfg, migrationFiles(files)))
	version, dirty := schemaVersion(t, traffic)
	assert.Equal(t, int64(2), version)
	assert.False(t, dirty)
}
