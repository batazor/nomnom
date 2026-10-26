// Package migration applies the schema migrations of the orders database
// (ADR 38, ADR 39). Every owner of tables — the pkg set of shared tables and
// each slice — has its own Set with its own numbering and version table; the
// composition root passes them to Apply in order, pkg first.
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"nomnom/orders/pkg/config"
)

// maxConns bounds the database/sql pool of one Set: golang-migrate takes its
// own (uncontended) advisory lock and applies migrations on one connection.
const maxConns = 1

// lockKey is the advisory lock shared by every orders migrator: "orders".
const lockKey int64 = 0x6f7264657273

// lockTimeout bounds every lock wait of a migration. Migrations run during a
// rolling update next to live traffic: an ALTER TABLE queued behind a long
// transaction would stall every query of the table behind it, so it fails
// fast and the pod retries on restart.
const lockTimeout = "5s"

// Set is the migrations of one owner: Files holds a migrations/ directory of
// NNNNN_name.up.sql files, Table is its golang-migrate version table.
type Set struct {
	Table string
	Files fs.FS
}

// Apply brings the schema to the sets in their order: applied migrations are
// skipped, missing ones applied. Every process calls it at startup. The sets
// are applied under a session advisory lock of its own connection, so
// processes starting at once serialize. The lock is taken before lock_timeout
// applies: the wait for another process must not be cut by it.
func Apply(ctx context.Context, cfg config.PostgresConfig, sets ...Set) error {
	if cfg.DSN == "" {
		return fmt.Errorf("%s is required", config.EnvPostgresDSN)
	}
	if err := validate(sets); err != nil {
		return err
	}
	connConfig, err := pgx.ParseConfig(cfg.DSN)
	if err != nil {
		return fmt.Errorf("parse %s: %w", config.EnvPostgresDSN, err)
	}
	if cfg.ConnectTimeout > 0 {
		connConfig.ConnectTimeout = cfg.ConnectTimeout
	}
	lock, err := pgx.ConnectConfig(ctx, connConfig.Copy())
	if err != nil {
		return fmt.Errorf("connect for the migration lock: %w", err)
	}
	// Closing the connection releases the lock with the session.
	defer func() { _ = lock.Close(context.WithoutCancel(ctx)) }()
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}

	migrationConfig := connConfig.Copy()
	migrationConfig.RuntimeParams["lock_timeout"] = lockTimeout
	for _, set := range sets {
		if err := applySet(ctx, cfg, migrationConfig, set); err != nil {
			return fmt.Errorf("migrations %s: %w", set.Table, err)
		}
	}
	return nil
}

// validate rejects a call without sets and sets sharing a version table: two
// owners in one table would see each other's versions as their own.
func validate(sets []Set) error {
	if len(sets) == 0 {
		return errors.New("no migration sets")
	}
	tables := make(map[string]bool, len(sets))
	for _, set := range sets {
		switch {
		case set.Table == "":
			return errors.New("migration set without a version table")
		case set.Files == nil:
			return fmt.Errorf("migration set %s without files", set.Table)
		case tables[set.Table]:
			return fmt.Errorf("migration sets share the version table %s", set.Table)
		}
		tables[set.Table] = true
	}
	return nil
}

// applySet runs one Set on a database/sql pool of its own: the driver closes
// the pool with the migrator.
func applySet(ctx context.Context, cfg config.PostgresConfig, connConfig *pgx.ConnConfig, set Set) error {
	db := stdlib.OpenDB(*connConfig)
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxLifetime(cfg.MaxConnLifetime)
	db.SetConnMaxIdleTime(cfg.MaxConnIdleTime)
	defer func() { _ = db.Close() }()

	migrator, files, err := newMigrator(db, set)
	if err != nil {
		return err
	}
	defer func() { _, _ = migrator.Close() }()
	if err := undoIncompleteMigration(ctx, migrator, files, set.Table); err != nil {
		return err
	}
	err = migrator.Up()
	applied := !errors.Is(err, migrate.ErrNoChange)
	if applied && err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	version, _, err := migrator.Version()
	if err != nil {
		return fmt.Errorf("migration version: %w", err)
	}
	slog.InfoContext(ctx, "postgres migrations checked", slog.String("table", set.Table),
		slog.Uint64("version", uint64(version)), slog.Bool("applied", applied))
	return nil
}

func newMigrator(db *sql.DB, set Set) (*migrate.Migrate, source.Driver, error) {
	files, err := iofs.New(set.Files, "migrations")
	if err != nil {
		return nil, nil, fmt.Errorf("migration files: %w", err)
	}
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{MigrationsTable: set.Table})
	if err != nil {
		return nil, nil, fmt.Errorf("migration database: %w", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", files, "pgx5", driver)
	if err != nil {
		return nil, nil, fmt.Errorf("migration provider: %w", err)
	}
	return migrator, files, nil
}

// undoIncompleteMigration returns a dirty version to the previous one.
// golang-migrate marks a version dirty before running its file and clean
// after; the pgx driver sends the file as one simple-protocol query, which
// PostgreSQL runs as one implicit transaction, so a file that failed or whose
// process died left no changes and the schema is at the previous version.
func undoIncompleteMigration(ctx context.Context, migrator *migrate.Migrate, files source.Driver, table string) error {
	version, dirty, err := migrator.Version()
	if errors.Is(err, migrate.ErrNilVersion) || (err == nil && !dirty) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migration version: %w", err)
	}
	previous := database.NilVersion
	prev, err := files.Prev(version)
	switch {
	case err == nil:
		previous = int(prev)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("migration before %d: %w", version, err)
	}
	if err := migrator.Force(previous); err != nil {
		return fmt.Errorf("undo incomplete migration %d: %w", version, err)
	}
	slog.WarnContext(ctx, "postgres migration did not complete, retrying it", slog.String("table", table),
		slog.Uint64("version", uint64(version)), slog.Int("previous", previous))
	return nil
}
