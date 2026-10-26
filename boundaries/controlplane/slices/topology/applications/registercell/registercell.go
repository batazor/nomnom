// Package registercell registers a Cell in the control plane (ADR 41). Only
// platform#manage_markets may do it. A repeat with the same values succeeds
// with Created false; other values for a registered cell_id are refused.
package registercell

import (
	"context"
	"fmt"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/permission"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
)

type Command struct {
	Subject        application.Subject
	CellID         string
	GatewayAddress string
}

type Result struct {
	Cell    cell.Cell
	Created bool
}

type Handler struct {
	authorizer application.Authorizer
	cells      cell.Repository
}

var _ application.Handler[Command, Result] = Handler{}

func New(authorizer application.Authorizer, cells cell.Repository) Handler {
	return Handler{authorizer: authorizer, cells: cells}
}

func (h Handler) Handle(ctx context.Context, cmd Command) (Result, error) {
	if err := permission.RequireManageMarkets(ctx, h.authorizer, cmd.Subject); err != nil {
		return Result{}, err
	}
	id, err := vo.NewCellID(cmd.CellID)
	if err != nil {
		return Result{}, err
	}
	gateway, err := vo.NewGatewayAddress(cmd.GatewayAddress)
	if err != nil {
		return Result{}, err
	}
	candidate := cell.New(id, gateway)
	added, err := h.cells.Add(ctx, candidate)
	if err != nil {
		return Result{}, fmt.Errorf("register cell %s: %w", id, err)
	}
	if added {
		return Result{Cell: candidate, Created: true}, nil
	}
	registered, err := h.cells.Get(ctx, id)
	if err != nil {
		return Result{}, fmt.Errorf("read registered cell %s: %w", id, err)
	}
	if err := candidate.Repeat(registered); err != nil {
		return Result{}, err
	}
	return Result{Cell: registered, Created: false}, nil
}
