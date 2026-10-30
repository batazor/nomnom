// Package grpcadapter is the gRPC API nomnom.controlplane.v1.ControlPlaneService
// (ADR 41): it authenticates the caller (ADR 27, ADR 32), maps messages to use
// cases and errors to gRPC statuses.
package grpcadapter

import (
	"context"

	controlplanev1 "github.com/batazor/nomnom/boundaries/controlplane/infrastructure/grpc/gen/nomnom/controlplane/v1"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/usecase"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/archivemarket"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/createmarket"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/listmarkets"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/registercell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

// Handlers are the use cases behind the API.
type Handlers struct {
	RegisterCell  usecase.Handler[registercell.Command, registercell.Result]
	CreateMarket  usecase.Handler[createmarket.Command, createmarket.Result]
	ArchiveMarket usecase.Handler[archivemarket.Command, archivemarket.Result]
	ListMarkets   usecase.Handler[listmarkets.Query, []market.Market]
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
		Subject: subject, CellID: req.GetCellId(), Router: registercell.Router{
			Namespace: req.GetRouter().GetNamespace(), Name: req.GetRouter().GetName(), Port: req.GetRouter().GetPort(),
		},
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
		Subject: subject, MarketID: req.GetMarketId(), Name: req.GetName(), Currency: req.GetCurrency(), TimeZone: req.GetTimeZone(), CellID: req.GetCellId(),
	})
	if err != nil {
		return nil, transportError(err)
	}
	return &controlplanev1.CreateMarketResponse{Market: toMarket(result.Market), Created: result.Created}, nil
}

func (s *Service) ArchiveMarket(ctx context.Context, req *controlplanev1.ArchiveMarketRequest) (*controlplanev1.ArchiveMarketResponse, error) {
	subject, err := s.subjects.Resolve(ctx)
	if err != nil {
		return nil, transportError(err)
	}
	result, err := s.handlers.ArchiveMarket.Handle(ctx, archivemarket.Command{Subject: subject, MarketID: req.GetMarketId()})
	if err != nil {
		return nil, transportError(err)
	}
	return &controlplanev1.ArchiveMarketResponse{Archived: result.Archived}, nil
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

func toCell(c cell.Cell) *controlplanev1.Cell {
	router := c.Router()
	return &controlplanev1.Cell{CellId: string(c.ID()), Router: &controlplanev1.ServiceRef{
		Namespace: router.Namespace(), Name: router.Name(), Port: router.Port(),
	}}
}

func toMarket(m market.Market) *controlplanev1.Market {
	return &controlplanev1.Market{MarketId: string(m.ID()), Name: string(m.Name()), Currency: string(m.Currency()), TimeZone: string(m.TimeZone()), CellId: string(m.CellID())}
}
