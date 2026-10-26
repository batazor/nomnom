// Package createmarket creates a Market placed in a registered Cell (ADR 41).
// Only platform#manage_markets may do it. A repeat with the same values
// succeeds with Created false; other values for an existing market_id are
// refused, and an unregistered cell_id is a failed precondition.
package createmarket

import (
	"context"
	"fmt"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/permission"
	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

type Command struct {
	Subject  application.Subject
	MarketID string
	Name     string
	Currency string
	CellID   string
}

type Result struct {
	Market  market.Market
	Created bool
}

type Handler struct {
	authorizer application.Authorizer
	markets    market.Repository
}

var _ application.Handler[Command, Result] = Handler{}

func New(authorizer application.Authorizer, markets market.Repository) Handler {
	return Handler{authorizer: authorizer, markets: markets}
}

func (h Handler) Handle(ctx context.Context, cmd Command) (Result, error) {
	if err := permission.RequireManageMarkets(ctx, h.authorizer, cmd.Subject); err != nil {
		return Result{}, err
	}
	candidate, err := parse(cmd)
	if err != nil {
		return Result{}, err
	}
	added, err := h.markets.Add(ctx, candidate)
	if err != nil {
		return Result{}, fmt.Errorf("create market %s: %w", candidate.ID(), err)
	}
	if added {
		return Result{Market: candidate, Created: true}, nil
	}
	existing, err := h.markets.Get(ctx, candidate.ID())
	if err != nil {
		return Result{}, fmt.Errorf("read market %s: %w", candidate.ID(), err)
	}
	if err := candidate.Repeat(existing); err != nil {
		return Result{}, err
	}
	return Result{Market: existing, Created: false}, nil
}

func parse(cmd Command) (market.Market, error) {
	id, err := vo.NewMarketID(cmd.MarketID)
	if err != nil {
		return market.Market{}, err
	}
	name, err := vo.NewName(cmd.Name)
	if err != nil {
		return market.Market{}, err
	}
	currency, err := vo.NewCurrency(cmd.Currency)
	if err != nil {
		return market.Market{}, err
	}
	cellID, err := cellvo.NewCellID(cmd.CellID)
	if err != nil {
		return market.Market{}, err
	}
	return market.New(id, name, currency, cellID), nil
}
