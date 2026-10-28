// Package archivemarket archives a Market (ADR 41): it leaves the list of
// Markets and the public gateway, its id stays taken. Only
// platform#manage_markets may do it. A repeat succeeds with Archived false;
// an unknown market_id is not found. After the archiving, including a repeat,
// the routes of the public gateway are published from the registry.
package archivemarket

import (
	"context"
	"fmt"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/permission"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

type Command struct {
	Subject  application.Subject
	MarketID string
}

type Result struct {
	Archived bool
}

// Archiver archives a Market; archived is false when it was archived already.
type Archiver interface {
	Archive(ctx context.Context, id vo.MarketID) (archived bool, err error)
}

type Handler struct {
	authorizer application.Authorizer
	markets    Archiver
	routes     publishroutes.Publisher
}

var _ application.Handler[Command, Result] = Handler{}

func New(authorizer application.Authorizer, markets Archiver, routes publishroutes.Publisher) Handler {
	return Handler{authorizer: authorizer, markets: markets, routes: routes}
}

func (h Handler) Handle(ctx context.Context, cmd Command) (Result, error) {
	if err := permission.RequireManageMarkets(ctx, h.authorizer, cmd.Subject); err != nil {
		return Result{}, err
	}
	id, err := vo.NewMarketID(cmd.MarketID)
	if err != nil {
		return Result{}, err
	}
	archived, err := h.markets.Archive(ctx, id)
	if err != nil {
		return Result{}, fmt.Errorf("archive market %s: %w", id, err)
	}
	if err := h.routes.Publish(ctx); err != nil {
		return Result{}, fmt.Errorf("publish routes after archiving market %s: %w", id, err)
	}
	return Result{Archived: archived}, nil
}
