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
	added, err := c.store.insertOnce(ctx, psql.Insert("cells").
		Columns("cell_id", "gateway_address", "registered_at").
		Values(string(candidate.ID()), string(candidate.GatewayAddress()), c.store.now().UTC()))
	if err != nil {
		return false, fmt.Errorf("insert cell: %w", err)
	}
	return added, nil
}

func (c Cells) Get(ctx context.Context, id vo.CellID) (cell.Cell, error) {
	var gateway string
	err := c.store.queryRow(ctx, psql.Select("gateway_address").From("cells").Where("cell_id = ?", string(id)), &gateway)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return cell.Cell{}, fmt.Errorf("%w: %s", cell.ErrNotFound, id)
	case err != nil:
		return cell.Cell{}, fmt.Errorf("select cell: %w", err)
	}
	return restoreCell(string(id), gateway)
}

// restoreCell rebuilds a stored Cell through its value objects: a row that
// breaks them is reported, not served.
func restoreCell(rawID, rawGateway string) (cell.Cell, error) {
	id, err := vo.NewCellID(rawID)
	if err != nil {
		return cell.Cell{}, fmt.Errorf("stored cell: %w", err)
	}
	gateway, err := vo.NewGatewayAddress(rawGateway)
	if err != nil {
		return cell.Cell{}, fmt.Errorf("stored cell %s: %w", id, err)
	}
	return cell.New(id, gateway), nil
}
