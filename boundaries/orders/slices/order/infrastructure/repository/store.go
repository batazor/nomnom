// Package repository implements the persistence ports of the order slice on
// PostgreSQL (ADR 8, ADR 20): Unit of Work, the Order repository with optimistic
// concurrency, Transactional Inbox, idempotency keys, read models, the
// authorization revision and the Transactional Outbox (ADR 13) in orders_outbox;
// the migrations/ of this package create all of these tables.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"nomnom/orders/pkg/config"
)

// psql builds statements with PostgreSQL $n placeholders.
var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Store shares one pool between the API, worker and consumer of
// orders. The transaction of a Unit of Work travels in context.
type Store struct {
	pool    *pgxpool.Pool
	metrics metric.Registration
	uow     *unitOfWorkMetrics
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
	uow, err := newUnitOfWorkMetrics(meter)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("unit of work metrics: %w", err)
	}
	registration, err := metrics.observe(meter, pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres pool metrics: %w", err)
	}
	return &Store{pool: pool, metrics: registration, uow: uow}, nil
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

func isUniqueViolation(err error) bool {
	var dbErr *pgconn.PgError
	return errors.As(err, &dbErr) && dbErr.Code == "23505"
}
