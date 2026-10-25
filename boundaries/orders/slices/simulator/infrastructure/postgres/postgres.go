// Package postgres stores the Simulator switch in the orders database
// (table simulator_state, migration 00006 of the order slice store).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"nomnom/orders/slices/simulator/applications/switchsimulator"
)

// psql builds statements with PostgreSQL $n placeholders.
var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

type State struct {
	pool *pgxpool.Pool
}

var _ switchsimulator.State = (*State)(nil)

func NewState(pool *pgxpool.Pool) *State {
	return &State{pool: pool}
}

// Enabled is false while no state is stored.
func (s *State) Enabled(ctx context.Context) (bool, error) {
	query, args, err := psql.Select("enabled").From("simulator_state").ToSql()
	if err != nil {
		return false, fmt.Errorf("build simulator_state query: %w", err)
	}
	var enabled bool
	err = s.pool.QueryRow(ctx, query, args...).Scan(&enabled)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("select simulator_state: %w", err)
	}
	return enabled, nil
}

func (s *State) SetEnabled(ctx context.Context, enabled bool) error {
	statement, args, err := psql.Insert("simulator_state").
		Columns("singleton", "enabled", "updated_at").Values(sq.Expr("TRUE"), enabled, time.Now().UTC()).
		Suffix("ON CONFLICT (singleton) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at").
		ToSql()
	if err != nil {
		return fmt.Errorf("build simulator_state upsert: %w", err)
	}
	if _, err := s.pool.Exec(ctx, statement, args...); err != nil {
		return fmt.Errorf("upsert simulator_state: %w", err)
	}
	return nil
}
