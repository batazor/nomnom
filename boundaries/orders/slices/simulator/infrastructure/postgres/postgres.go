// Package postgres stores the Simulator switch in the orders database
// (table simulator_state, migration 00006 of the order slice store).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"nomnom/orders/slices/simulator/applications/switchsimulator"
)

type State struct {
	pool *pgxpool.Pool
}

var _ switchsimulator.State = (*State)(nil)

func NewState(pool *pgxpool.Pool) *State {
	return &State{pool: pool}
}

// Enabled is false while no state is stored.
func (s *State) Enabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `SELECT enabled FROM simulator_state`).Scan(&enabled)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("select simulator_state: %w", err)
	}
	return enabled, nil
}

func (s *State) SetEnabled(ctx context.Context, enabled bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO simulator_state (singleton, enabled, updated_at) VALUES (TRUE, $1, $2)
		ON CONFLICT (singleton) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at`,
		enabled, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("upsert simulator_state: %w", err)
	}
	return nil
}
