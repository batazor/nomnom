// Package listmarkets returns every Market of the registry that is not
// archived (ADR 41). Only
// platform#manage_markets — the platform operator — reads it; clients get the
// Markets from the public gateway, not from the control plane.
package listmarkets

import (
	"context"
	"fmt"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/authz"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/usecase"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/permission"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

type Query struct {
	Subject authz.Subject
}

// Lister reads every Market that is not archived, by name, then by id.
type Lister interface {
	List(ctx context.Context) ([]market.Market, error)
}

type Handler struct {
	authorizer authz.Authorizer
	markets    Lister
}

var _ usecase.Handler[Query, []market.Market] = Handler{}

func New(authorizer authz.Authorizer, markets Lister) Handler {
	return Handler{authorizer: authorizer, markets: markets}
}

func (h Handler) Handle(ctx context.Context, query Query) ([]market.Market, error) {
	if err := permission.RequireManageMarkets(ctx, h.authorizer, query.Subject); err != nil {
		return nil, err
	}
	markets, err := h.markets.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list markets: %w", err)
	}
	return markets, nil
}
