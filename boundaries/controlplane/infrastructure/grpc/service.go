// Package grpcadapter is the gRPC API nomnom.controlplane.v1.ControlPlaneService
// (ADR 41): it authenticates the caller (ADR 27, ADR 32), maps messages to use
// cases and errors to gRPC statuses.
package grpcadapter

import (
	"context"

	controlplanev1 "github.com/batazor/nomnom/boundaries/controlplane/infrastructure/grpc/gen/nomnom/controlplane/v1"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/createmarket"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/getplacementmap"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/listmarkets"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/registercell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

// Handlers are the use cases behind the API.
type Handlers struct {
	RegisterCell    application.Handler[registercell.Command, registercell.Result]
	CreateMarket    application.Handler[createmarket.Command, createmarket.Result]
	ListMarkets     application.Handler[listmarkets.Query, []market.Market]
	GetPlacementMap application.Handler[getplacementmap.Query, []getplacementmap.Placement]
}

type Service struct {
	controlplanev1.UnimplementedControlPlaneServiceServer
	handlers Handlers
	subjects SubjectResolver
}

func NewService(handlers Handlers, subjects SubjectResolver) *Service {
	return &Service{handlers: handlers, subjects: subjects}
}

func (s *Service) RegisterCell(ctx context.Context, req *controlplanev1.RegisterCellRequest) (*controlplanev1.RegisterCellResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	result, err := s.handlers.RegisterCell.Handle(ctx, registercell.Command{
		Subject: subject, CellID: req.GetCellId(), GatewayAddress: req.GetGatewayAddress(),
	})
	if err != nil {
		return nil, transportError(err)
	}
	return &controlplanev1.RegisterCellResponse{Cell: toCell(result.Cell), Created: result.Created}, nil
}

func (s *Service) CreateMarket(ctx context.Context, req *controlplanev1.CreateMarketRequest) (*controlplanev1.CreateMarketResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	result, err := s.handlers.CreateMarket.Handle(ctx, createmarket.Command{
		Subject: subject, MarketID: req.GetMarketId(), Name: req.GetName(), Currency: req.GetCurrency(), CellID: req.GetCellId(),
	})
	if err != nil {
		return nil, transportError(err)
	}
	return &controlplanev1.CreateMarketResponse{Market: toMarket(result.Market), Created: result.Created}, nil
}

func (s *Service) ListMarkets(ctx context.Context, _ *controlplanev1.ListMarketsRequest) (*controlplanev1.ListMarketsResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	markets, err := s.handlers.ListMarkets.Handle(ctx, listmarkets.Query{Subject: subject})
	if err != nil {
		return nil, transportError(err)
	}
	resp := &controlplanev1.ListMarketsResponse{Markets: make([]*controlplanev1.Market, 0, len(markets))}
	for _, m := range markets {
		resp.Markets = append(resp.Markets, toMarket(m))
	}
	return resp, nil
}

func (s *Service) GetPlacementMap(ctx context.Context, _ *controlplanev1.GetPlacementMapRequest) (*controlplanev1.GetPlacementMapResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	placements, err := s.handlers.GetPlacementMap.Handle(ctx, getplacementmap.Query{Subject: subject})
	if err != nil {
		return nil, transportError(err)
	}
	resp := &controlplanev1.GetPlacementMapResponse{Placements: make([]*controlplanev1.Placement, 0, len(placements))}
	for _, p := range placements {
		resp.Placements = append(resp.Placements, &controlplanev1.Placement{MarketId: p.MarketID, CellId: p.CellID, GatewayAddress: p.GatewayAddress})
	}
	return resp, nil
}

func toCell(c cell.Cell) *controlplanev1.Cell {
	return &controlplanev1.Cell{CellId: string(c.ID()), GatewayAddress: string(c.GatewayAddress())}
}

func toMarket(m market.Market) *controlplanev1.Market {
	return &controlplanev1.Market{MarketId: string(m.ID()), Name: string(m.Name()), Currency: string(m.Currency()), CellId: string(m.CellID())}
}
