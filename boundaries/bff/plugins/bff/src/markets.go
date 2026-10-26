package main

import (
	"context"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/markets/applications"
)

type listMarketsHandler interface {
	Handle(ctx context.Context) ([]applications.Market, error)
}

// QueryMarkets is public and needs no X-Nomnom-Market: the client chooses its
// Market from this list (ADR 41).
func (s *BffService) QueryMarkets(ctx context.Context, _ *service.QueryMarketsRequest) (*service.QueryMarketsResponse, error) {
	markets, err := s.listMarkets.Handle(ctx)
	if err != nil {
		return nil, internalError("markets", err)
	}
	response := &service.QueryMarketsResponse{Markets: make([]*service.Market, 0, len(markets))}
	for _, market := range markets {
		response.Markets = append(response.Markets, &service.Market{Id: market.ID, Name: market.Name, Currency: market.Currency})
	}
	return response, nil
}
