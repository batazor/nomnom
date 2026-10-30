// Package cell is the Cell of the control plane (ADR 41): a complete isolated
// instance of the data plane, known by its id and its Router — the Service the
// public gateway routes its Markets to.
package cell

import (
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
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
