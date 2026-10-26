// Package listmarkets returns every Market for the Market choice of the client
// (ADR 41). Only allowlisted services — the service identity of BFF — read it.
package listmarkets

import (
	"context"
	"fmt"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

type Query struct {
	Subject application.Subject
}

// Lister reads every Market by name, then by id.
type Lister interface {
	List(ctx context.Context) ([]market.Market, error)
}

type Handler struct {
	readers application.ServiceAllowlist
	markets Lister
}

var _ application.Handler[Query, []market.Market] = Handler{}

func New(readers application.ServiceAllowlist, markets Lister) Handler {
	return Handler{readers: readers, markets: markets}
}

func (h Handler) Handle(ctx context.Context, query Query) ([]market.Market, error) {
	if err := h.readers.Allow(query.Subject); err != nil {
		return nil, err
	}
	markets, err := h.markets.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list markets: %w", err)
	}
	return markets, nil
}
