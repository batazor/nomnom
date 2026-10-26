package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
)

// Cells is the cell.Repository on table cells.
type Cells struct{ store *Store }

var _ cell.Repository = Cells{}

func NewCells(store *Store) Cells { return Cells{store: store} }

func (c Cells) Add(ctx context.Context, candidate cell.Cell) (bool, error) {
	router := candidate.Router()
	added, err := c.store.insertOnce(ctx, psql.Insert("cells").
		Columns("cell_id", "router_namespace", "router_name", "router_port", "registered_at").
		Values(string(candidate.ID()), router.Namespace(), router.Name(), router.Port(), c.store.now().UTC()))
	if err != nil {
		return false, fmt.Errorf("insert cell: %w", err)
	}
	return added, nil
}

func (c Cells) Get(ctx context.Context, id vo.CellID) (cell.Cell, error) {
	var row routerRow
	err := c.store.queryRow(ctx, psql.Select(routerColumns("")...).From("cells").Where("cell_id = ?", string(id)), row.dest()...)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return cell.Cell{}, fmt.Errorf("%w: %s", cell.ErrNotFound, id)
	case err != nil:
		return cell.Cell{}, fmt.Errorf("select cell: %w", err)
	}
	cellID, err := vo.NewCellID(string(id))
	if err != nil {
		return cell.Cell{}, fmt.Errorf("stored cell: %w", err)
	}
	router, err := row.restore()
	if err != nil {
		return cell.Cell{}, fmt.Errorf("stored cell %s: %w", cellID, err)
	}
	return cell.New(cellID, router), nil
}

// routerColumns are the Router columns of cells, qualified by alias when one is given.
func routerColumns(alias string) []string {
	columns := []string{"router_namespace", "router_name", "router_port"}
	if alias == "" {
		return columns
	}
	for i, column := range columns {
		columns[i] = alias + "." + column
	}
	return columns
}

type routerRow struct {
	namespace, name string
	port            int32
}

func (r *routerRow) dest() []any { return []any{&r.namespace, &r.name, &r.port} }

// restore rebuilds a stored Router through its value object: a row that breaks
// it is reported, not served.
func (r routerRow) restore() (vo.Router, error) {
	return vo.NewRouter(r.namespace, r.name, r.port)
}
