package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
)

// routesLockKey is the transaction-level advisory lock of route publication:
// one publisher at a time reads the registry and applies it.
// The key is unique within the database of the control plane.
const routesLockKey int64 = 410001

// Routes is the registry snapshot of route publication: the Markets that are
// not archived joined with the Router of their Cell.
type Routes struct{ store *Store }

var _ publishroutes.Registry = Routes{}

func NewRoutes(store *Store) Routes { return Routes{store: store} }

// Locked holds the advisory lock in a read-only transaction while publish
// runs: the lock goes with the transaction, so it is never left on a pooled
// connection.
func (r Routes) Locked(ctx context.Context, publish func(context.Context, []publishroutes.MarketRoute) error) (err error) {
	tx, err := r.store.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin routes snapshot: %w", err)
	}
	defer func() { err = errors.Join(err, rollback(tx)) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", routesLockKey); err != nil {
		return fmt.Errorf("lock routes: %w", err)
	}
	routes, err := r.snapshot(ctx, tx)
	if err != nil {
		return err
	}
	return publish(ctx, routes)
}

func (r Routes) snapshot(ctx context.Context, tx pgx.Tx) ([]publishroutes.MarketRoute, error) {
	columns := append(qualified("m", marketColumns), routerColumns("c")...)
	sql, args, err := psql.Select(columns...).From("markets m").Join("cells c ON c.cell_id = m.cell_id").
		Where("m.archived_at IS NULL").OrderBy("m.name", "m.market_id").ToSql()
	if err != nil {
		return nil, fmt.Errorf("build routes query: %w", err)
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("select routes: %w", err)
	}
	defer rows.Close()
	var routes []publishroutes.MarketRoute
	for rows.Next() {
		var market marketRow
		var router routerRow
		if err := rows.Scan(append(market.dest(), router.dest()...)...); err != nil {
			return nil, fmt.Errorf("scan route: %w", err)
		}
		restored, err := market.restore()
		if err != nil {
			return nil, err
		}
		restoredRouter, err := router.restore()
		if err != nil {
			return nil, fmt.Errorf("stored cell %s: %w", restored.CellID(), err)
		}
		routes = append(routes, publishroutes.MarketRoute{Market: restored, Router: restoredRouter})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read routes: %w", err)
	}
	return routes, nil
}

func qualified(alias string, columns []string) []string {
	out := make([]string, len(columns))
	for i, column := range columns {
		out[i] = alias + "." + column
	}
	return out
}

// rollback ends the read-only snapshot; a transaction already closed is fine.
func rollback(tx pgx.Tx) error {
	if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return fmt.Errorf("end routes snapshot: %w", err)
	}
	return nil
}
