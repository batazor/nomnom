package migration_test

import (
	"context"
	"net/url"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/migration"
	"nomnom/orders/pkg/testfixture"
)

// pg is the database server of the package; every test migrates a database
// of its own.
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

func set(table string, files map[string]string) migration.Set {
	fsys := fstest.MapFS{}
	for name, sql := range files {
		fsys["migrations/"+name] = &fstest.MapFile{Data: []byte(sql)}
	}
	return migration.Set{Table: table, Files: fsys}
}

func schemaVersion(t *testing.T, conn *pgx.Conn, table string) (int64, bool) {
	t.Helper()
	var (
		version int64
		dirty   bool
	)
	require.NoError(t, conn.QueryRow(context.Background(),
		`SELECT version, dirty FROM `+pgx.Identifier{table}.Sanitize()).Scan(&version, &dirty))
	return version, dirty
}

func tableExists(t *testing.T, conn *pgx.Conn, name string) bool {
	t.Helper()
	var exists bool
	require.NoError(t, conn.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists))
	return exists
}

// Every set keeps its own numbering in its own version table; they are applied
// in the given order, and a failed set leaves the sets before it applied.
func TestApplyKeepsEverySetInItsOwnVersionTable(t *testing.T) {
	ctx := context.Background()
	cfg := config.PostgresConfig{DSN: emptyDatabase(t)}
	shared := set("schema_migrations_shared", map[string]string{"1_shared.up.sql": `CREATE TABLE shared (id int);`})
	slice := map[string]string{
		"1_a.up.sql": `CREATE TABLE a (id int);`,
		"2_b.up.sql": `CREATE TABLE b (shared_id int); SELECT 1 / 0;`,
	}
	require.Error(t, migration.Apply(ctx, cfg, shared, set("schema_migrations_slice", slice)))

	conn, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	version, dirty := schemaVersion(t, conn, "schema_migrations_shared")
	assert.Equal(t, int64(1), version)
	assert.False(t, dirty, "the set before the failed one is applied")

	slice["2_b.up.sql"] = `CREATE TABLE b (shared_id int);`
	require.NoError(t, migration.Apply(ctx, cfg, shared, set("schema_migrations_slice", slice)))
	version, dirty = schemaVersion(t, conn, "schema_migrations_slice")
	assert.Equal(t, int64(2), version)
	assert.False(t, dirty)
	assert.True(t, tableExists(t, conn, "b"))
	assert.False(t, tableExists(t, conn, "schema_migrations"), "no set uses the default version table")
}

// Sets in one version table would read each other's versions as their own.
func TestApplyRejectsSetsSharingAVersionTable(t *testing.T) {
	ctx := context.Background()
	cfg := config.PostgresConfig{DSN: emptyDatabase(t)}
	a := set("schema_migrations_a", map[string]string{"1_a.up.sql": `CREATE TABLE a (id int);`})

	require.ErrorContains(t, migration.Apply(ctx, cfg), "no migration sets")
	require.ErrorContains(t, migration.Apply(ctx, cfg, a, a), "share the version table schema_migrations_a")
	require.ErrorContains(t, migration.Apply(ctx, cfg, migration.Set{Files: a.Files}), "without a version table")
	require.ErrorContains(t, migration.Apply(ctx, cfg, migration.Set{Table: "schema_migrations_a"}), "without files")
}

// A failed migration file leaves no changes (one implicit transaction) and a
// dirty version; the next start returns it to the previous version and
// applies the file again instead of refusing to start.
func TestApplyRetriesAnIncompleteMigration(t *testing.T) {
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
			require.Error(t, migration.Apply(ctx, cfg, set("schema_migrations_test", files)))

			conn, err := pgx.Connect(ctx, cfg.DSN)
			require.NoError(t, err)
			defer func() { _ = conn.Close(ctx) }()
			_, dirty := schemaVersion(t, conn, "schema_migrations_test")
			require.True(t, dirty)
			assert.False(t, tableExists(t, conn, "a"), "the failed file left no changes")

			files[tc.first] = `CREATE TABLE a (id int);`
			require.NoError(t, migration.Apply(ctx, cfg, set("schema_migrations_test", files)))
			version, dirty := schemaVersion(t, conn, "schema_migrations_test")
			assert.False(t, dirty)
			assert.Equal(t, int64(len(files)), version)
			assert.True(t, tableExists(t, conn, "a"))
		})
	}
}

// A migration waiting for a lock held by live traffic fails within
// lock_timeout instead of stalling the table, and succeeds on the next start.
func TestApplyGivesUpOnALockedTable(t *testing.T) {
	ctx := context.Background()
	cfg := config.PostgresConfig{DSN: emptyDatabase(t)}
	files := map[string]string{"1_a.up.sql": `CREATE TABLE a (id int);`}
	require.NoError(t, migration.Apply(ctx, cfg, set("schema_migrations_test", files)))

	traffic, err := pgx.Connect(ctx, cfg.DSN)
	require.NoError(t, err)
	defer func() { _ = traffic.Close(ctx) }()
	tx, err := traffic.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT * FROM a`)
	require.NoError(t, err)

	files["2_column.up.sql"] = `ALTER TABLE a ADD COLUMN name text;`
	started := time.Now()
	err = migration.Apply(ctx, cfg, set("schema_migrations_test", files))
	// golang-migrate wraps the driver error without Unwrap.
	require.ErrorContains(t, err, "SQLSTATE 55P03", "lock_not_available")
	assert.Less(t, time.Since(started), 30*time.Second)

	require.NoError(t, tx.Rollback(ctx))
	require.NoError(t, migration.Apply(ctx, cfg, set("schema_migrations_test", files)))
	version, dirty := schemaVersion(t, traffic, "schema_migrations_test")
	assert.Equal(t, int64(2), version)
	assert.False(t, dirty)
}
