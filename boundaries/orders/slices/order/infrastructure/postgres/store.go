// Package postgres implements the persistence ports of the order slice on
// PostgreSQL (ADR 8, ADR 20): Unit of Work, the Order repository with optimistic
// concurrency, Transactional Inbox, idempotency keys, read models, the
// authorization revision and the Transactional Outbox (ADR 13).
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"nomnom/orders/pkg/config"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrateMaxConns bounds the database/sql pool of Migrate: golang-migrate
// takes the session advisory lock and applies migrations on one connection.
const migrateMaxConns = 1

// migrateLockTimeout bounds the wait for the advisory lock held by another
// process applying migrations at the same start.
const migrateLockTimeout = 5 * time.Minute

// psql builds statements with PostgreSQL $n placeholders.
var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Store shares one pool between the API, worker and consumer of
// orders. The transaction of a Unit of Work travels in context.
type Store struct {
	pool    *pgxpool.Pool
	metrics metric.Registration
}

// Open creates the pool; it does not connect until the first query.
func Open(ctx context.Context, cfg config.PostgresConfig) (*Store, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("%s is required: orders keeps its state in PostgreSQL", config.EnvPostgresDSN)
	}
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", config.EnvPostgresDSN, err)
	}
	applyPoolLimits(poolConfig, cfg)
	meter := otel.Meter(poolInstrumentation)
	metrics, err := newPoolMetrics(meter, time.Now)
	if err != nil {
		return nil, fmt.Errorf("postgres pool metrics: %w", err)
	}
	poolConfig.ConnConfig.Tracer = connTracer(metrics)
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	registration, err := metrics.observe(meter, pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres pool metrics: %w", err)
	}
	return &Store{pool: pool, metrics: registration}, nil
}

// applyPoolLimits sizes the per-process pool behind the session-mode pooler:
// each pooled connection pins one server connection, so MaxConns of every
// replica counts against the pooler pool_size. The DSN keeps the default
// prepared-statement cache, which needs session mode. Zero durations (configs
// built in tests) keep the pgx defaults; Load always sets them.
func applyPoolLimits(poolConfig *pgxpool.Config, cfg config.PostgresConfig) {
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetimeJitter = cfg.MaxConnLifetimeJitter
	for target, value := range map[*time.Duration]time.Duration{
		&poolConfig.MaxConnLifetime:           cfg.MaxConnLifetime,
		&poolConfig.MaxConnIdleTime:           cfg.MaxConnIdleTime,
		&poolConfig.HealthCheckPeriod:         cfg.HealthCheckPeriod,
		&poolConfig.ConnConfig.ConnectTimeout: cfg.ConnectTimeout,
	} {
		if value > 0 {
			*target = value
		}
	}
}

func (s *Store) Close() {
	_ = s.metrics.Unregister()
	s.pool.Close()
}

// Pool exposes the pool for test fixtures and read-only checks.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Migrate brings the schema to the embedded migrations under a PostgreSQL
// advisory lock: applied ones are skipped, missing ones applied, so processes
// starting at once serialize. Every process calls it at startup.
func Migrate(ctx context.Context, cfg config.PostgresConfig) error {
	if cfg.DSN == "" {
		return fmt.Errorf("%s is required", config.EnvPostgresDSN)
	}
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return fmt.Errorf("parse %s: %w", config.EnvPostgresDSN, err)
	}
	if cfg.ConnectTimeout > 0 {
		poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}
	db := stdlib.OpenDB(*poolConfig.ConnConfig)
	db.SetMaxOpenConns(migrateMaxConns)
	db.SetMaxIdleConns(migrateMaxConns)
	db.SetConnMaxLifetime(cfg.MaxConnLifetime)
	db.SetConnMaxIdleTime(cfg.MaxConnIdleTime)
	defer func() { _ = db.Close() }()

	files, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migration files: %w", err)
	}
	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("migration database: %w", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", files, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("migration provider: %w", err)
	}
	defer func() { _, _ = migrator.Close() }()
	migrator.LockTimeout = migrateLockTimeout
	err = migrator.Up()
	applied := !errors.Is(err, migrate.ErrNoChange)
	if applied && err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	version, _, err := migrator.Version()
	if err != nil {
		return fmt.Errorf("migration version: %w", err)
	}
	slog.InfoContext(ctx, "postgres migrations checked", slog.Uint64("version", uint64(version)), slog.Bool("applied", applied))
	return nil
}

type txKey struct{}

// querier is the active transaction or the pool.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Store) q(ctx context.Context) querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return s.pool
}

// exec runs the built statement in the active transaction or on the pool.
func (s *Store) exec(ctx context.Context, stmt sq.Sqlizer) (pgconn.CommandTag, error) {
	sql, args, err := stmt.ToSql()
	if err != nil {
		return pgconn.CommandTag{}, fmt.Errorf("build statement: %w", err)
	}
	return s.q(ctx).Exec(ctx, sql, args...)
}

// query runs the built query in the active transaction or on the pool.
func (s *Store) query(ctx context.Context, stmt sq.Sqlizer) (pgx.Rows, error) {
	sql, args, err := stmt.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	return s.q(ctx).Query(ctx, sql, args...)
}

// queryRow scans the single row of the built query into dest; no row is
// pgx.ErrNoRows.
func (s *Store) queryRow(ctx context.Context, stmt sq.Sqlizer, dest ...any) error {
	sql, args, err := stmt.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}
	return s.q(ctx).QueryRow(ctx, sql, args...).Scan(dest...)
}

func transaction(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// Do runs fn in one READ COMMITTED transaction; a nested Do joins the outer
// one. An error or panic rolls back; a panic is re-raised.
func (s *Store) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := transaction(ctx); ok {
		return fn(ctx)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(recovered)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
