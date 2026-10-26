// Package cell is the Cell of the control plane (ADR 41): a complete isolated
// instance of the data plane, known by its id and its Router — the Service the
// public gateway routes its Markets to. Both are immutable: a registered Cell
// is never changed.
package cell

import (
	"context"
	"errors"
	"fmt"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
)

var (
	// ErrAlreadyRegistered: a Cell with the id is registered with other values.
	ErrAlreadyRegistered = errors.New("cell already registered with other values")
	// ErrNotFound: no Cell has the id.
	ErrNotFound = errors.New("cell not found")
)

type Cell struct {
	id     vo.CellID
	router vo.Router
}

func New(id vo.CellID, router vo.Router) Cell {
	return Cell{id: id, router: router}
}

func (c Cell) ID() vo.CellID     { return c.id }
func (c Cell) Router() vo.Router { return c.router }

// Repeat decides a registration of a Cell whose id is registered already: the
// same values are the same Cell (an idempotent repeat), other values conflict.
func (c Cell) Repeat(registered Cell) error {
	if c != registered {
		return fmt.Errorf("%w: %s has router %s", ErrAlreadyRegistered, registered.id, registered.router)
	}
	return nil
}

// Repository stores registered Cells.
type Repository interface {
	// Add registers the Cell unless its id is registered; added is false then.
	Add(ctx context.Context, cell Cell) (added bool, err error)
	// Get returns the Cell or ErrNotFound.
	Get(ctx context.Context, id vo.CellID) (Cell, error)
}
