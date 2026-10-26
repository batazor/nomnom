// Package repository implements the persistence ports of the topology slice on
// PostgreSQL (ADR 8, ADR 39): the Cell and Market repositories and the
// Placement Map read model. The migrations/ of this package create the tables.
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config/database"
)

// psql builds statements with PostgreSQL $n placeholders.
var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// foreignKeyViolation is SQLSTATE 23503.
const foreignKeyViolation = "23503"

// Store owns the pool of the process.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// Open creates the pool; it does not connect until the first query. Queries
// are traced by otelpgx without statement text and bind parameters.
func Open(ctx context.Context, cfg database.Config) (*Store, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("%s is required: controlplane keeps its registry in PostgreSQL", database.EnvPostgresDSN)
	}
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", database.EnvPostgresDSN, err)
	}
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetimeJitter = cfg.MaxConnLifetimeJitter
	for target, value := range map[*time.Duration]time.Duration{
		&poolConfig.MaxConnLifetime:           cfg.MaxConnLifetime,
		&poolConfig.MaxConnIdleTime:           cfg.MaxConnIdleTime,
		&poolConfig.HealthCheckPeriod:         cfg.HealthCheckPeriod,
		&poolConfig.ConnConfig.ConnectTimeout: cfg.ConnectTimeout,
	} {
		// Zero durations come from configs built in tests; Load always sets them.
		if value > 0 {
			*target = value
		}
	}
	poolConfig.ConnConfig.Tracer = otelpgx.NewTracer(
		otelpgx.WithDisableSQLStatementInAttributes(),
		otelpgx.WithMetricOperationName(otelpgx.SQLOperationName),
		otelpgx.WithDisableAcquireTracer(),
	)
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	return &Store{pool: pool, now: time.Now}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Pool exposes the pool for test fixtures.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// insertOnce runs an INSERT … ON CONFLICT DO NOTHING; added reports a new row.
func (s *Store) insertOnce(ctx context.Context, stmt sq.InsertBuilder) (bool, error) {
	sql, args, err := stmt.Suffix("ON CONFLICT DO NOTHING").ToSql()
	if err != nil {
		return false, fmt.Errorf("build statement: %w", err)
	}
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) queryRow(ctx context.Context, stmt sq.Sqlizer, dest ...any) error {
	sql, args, err := stmt.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}
	return s.pool.QueryRow(ctx, sql, args...).Scan(dest...)
}

func (s *Store) query(ctx context.Context, stmt sq.Sqlizer) (pgx.Rows, error) {
	sql, args, err := stmt.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	return s.pool.Query(ctx, sql, args...)
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation
}
