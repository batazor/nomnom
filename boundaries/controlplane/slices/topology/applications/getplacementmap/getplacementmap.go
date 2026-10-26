// Package getplacementmap returns the Placement Map (ADR 41): for every Market
// its Cell and the address of the internal gateway of that Cell — what the
// router (BFF) needs to send a request of a Market to its Cell. Only
// allowlisted services — the service identity of BFF — read it.
package getplacementmap

import (
	"context"
	"fmt"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
)

type Query struct {
	Subject application.Subject
}

// Placement is the read model of one Market Placement.
type Placement struct {
	MarketID       string
	CellID         string
	GatewayAddress string
}

// Reader reads every Market Placement by market_id.
type Reader interface {
	Placements(ctx context.Context) ([]Placement, error)
}

type Handler struct {
	readers    application.ServiceAllowlist
	placements Reader
}

var _ application.Handler[Query, []Placement] = Handler{}

func New(readers application.ServiceAllowlist, placements Reader) Handler {
	return Handler{readers: readers, placements: placements}
}

func (h Handler) Handle(ctx context.Context, query Query) ([]Placement, error) {
	if err := h.readers.Allow(query.Subject); err != nil {
		return nil, err
	}
	placements, err := h.placements.Placements(ctx)
	if err != nil {
		return nil, fmt.Errorf("read placement map: %w", err)
	}
	return placements, nil
}
