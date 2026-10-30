// Package publishroutes brings the routes of the public gateway to the registry
// (ADR 41): a route per Market to the Router of its Cell and the list of Markets
// for the client. A Market the control plane cannot serve — its Cell is not
// declared or its values are invalid — stays off the gateway and does not stop
// the others. The outcome of every Cell and Market goes back to the registry.
package publishroutes

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	marketvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market/vo"
)

// ErrRoutesUnavailable: the routes were not applied. The caller repeats the
// publication; the registry stays as declared.
var ErrRoutesUnavailable = errors.New("routes of the public gateway not applied")

// Reason says why a resource of the registry is or is not ready.
type Reason string

const (
	ReasonAccepted          Reason = "Accepted"
	ReasonRoutesApplied     Reason = "RoutesApplied"
	ReasonRoutesUnavailable Reason = "RoutesUnavailable"
	ReasonCellNotFound      Reason = "CellNotFound"
	ReasonInvalidTimeZone   Reason = "InvalidTimeZone"
	ReasonInvalidValue      Reason = "InvalidValue"
	ReasonArchived          Reason = "Archived"
)

// DeclaredCell is a Cell as the registry declares it.
type DeclaredCell struct {
	ID              string
	RouterNamespace string
	RouterName      string
	RouterPort      int32
}

// DeclaredMarket is a Market as the registry declares it.
type DeclaredMarket struct {
	ID       string
	Name     string
	Currency string
	TimeZone string
	CellID   string
	Archived bool
}

// Topology is the snapshot of the registry.
type Topology struct {
	Cells   []DeclaredCell
	Markets []DeclaredMarket
}

// Outcome is the result of the publication for one resource.
type Outcome struct {
	Ready   bool
	Reason  Reason
	Message string
}

// Outcomes are the results by the id of a Cell and of a Market.
type Outcomes struct {
	Cells   map[string]Outcome
	Markets map[string]Outcome
}

// MarketRoute is a Market with the Router of its Market Placement.
type MarketRoute struct {
	Market market.Market
	Router cellvo.Router
}

// Registry reads the declared topology and records the outcome of the
// publication.
type Registry interface {
	Read(ctx context.Context) (Topology, error)
	Record(ctx context.Context, outcomes Outcomes) error
}

// Applier applies the whole set of routes to the public gateway; a Market
// route outside the set is removed.
type Applier interface {
	Apply(ctx context.Context, routes []MarketRoute) error
}

type Handler struct {
	registry Registry
	applier  Applier
}

func New(registry Registry, applier Applier) Handler {
	return Handler{registry: registry, applier: applier}
}

// Publish applies the routes of the current registry and records the outcome,
// also after a failure, so the operator sees it. A returned error means the
// publication must be repeated.
func (h Handler) Publish(ctx context.Context) error {
	topology, err := h.registry.Read(ctx)
	if err != nil {
		return fmt.Errorf("read registry: %w", err)
	}
	routes, outcomes := plan(topology)

	var applyErr error
	if err := h.applier.Apply(ctx, routes); err != nil {
		applyErr = fmt.Errorf("%w: %w", ErrRoutesUnavailable, err)
		for _, route := range routes {
			outcomes.Markets[string(route.Market.ID())] = Outcome{Reason: ReasonRoutesUnavailable, Message: err.Error()}
		}
	}
	return errors.Join(applyErr, h.registry.Record(ctx, outcomes))
}

// plan decides the routes and the outcome of every resource from the snapshot.
// The routes go by name, then by id: the order of the list of Markets.
func plan(topology Topology) ([]MarketRoute, Outcomes) {
	outcomes := Outcomes{Cells: map[string]Outcome{}, Markets: map[string]Outcome{}}
	cells := map[cellvo.CellID]cell.Cell{}
	for _, declared := range topology.Cells {
		parsed, err := parseCell(declared)
		if err != nil {
			outcomes.Cells[declared.ID] = Outcome{Reason: ReasonInvalidValue, Message: err.Error()}
			continue
		}
		cells[parsed.ID()] = parsed
		outcomes.Cells[declared.ID] = Outcome{Ready: true, Reason: ReasonAccepted, Message: "the Cell is accepted"}
	}

	routes := []MarketRoute{}
	for _, declared := range topology.Markets {
		if declared.Archived {
			outcomes.Markets[declared.ID] = Outcome{Reason: ReasonArchived, Message: "the Market is archived"}
			continue
		}
		parsed, reason, err := parseMarket(declared)
		if err != nil {
			outcomes.Markets[declared.ID] = Outcome{Reason: reason, Message: err.Error()}
			continue
		}
		placement, found := cells[parsed.CellID()]
		if !found {
			outcomes.Markets[declared.ID] = Outcome{Reason: ReasonCellNotFound, Message: fmt.Sprintf("Cell %s is not declared", parsed.CellID())}
			continue
		}
		routes = append(routes, MarketRoute{Market: parsed, Router: placement.Router()})
		outcomes.Markets[declared.ID] = Outcome{Ready: true, Reason: ReasonRoutesApplied, Message: "the routes of the Market are applied"}
	}
	slices.SortFunc(routes, func(a, b MarketRoute) int {
		return cmp.Or(cmp.Compare(a.Market.Name(), b.Market.Name()), cmp.Compare(a.Market.ID(), b.Market.ID()))
	})
	return routes, outcomes
}

func parseCell(declared DeclaredCell) (cell.Cell, error) {
	id, err := cellvo.NewCellID(declared.ID)
	if err != nil {
		return cell.Cell{}, err
	}
	router, err := cellvo.NewRouter(declared.RouterNamespace, declared.RouterName, declared.RouterPort)
	if err != nil {
		return cell.Cell{}, err
	}
	return cell.New(id, router), nil
}

func parseMarket(declared DeclaredMarket) (market.Market, Reason, error) {
	timeZone, err := marketvo.NewTimeZone(declared.TimeZone)
	if err != nil {
		return market.Market{}, ReasonInvalidTimeZone, err
	}
	id, err := marketvo.NewMarketID(declared.ID)
	if err != nil {
		return market.Market{}, ReasonInvalidValue, err
	}
	name, err := marketvo.NewName(declared.Name)
	if err != nil {
		return market.Market{}, ReasonInvalidValue, err
	}
	currency, err := marketvo.NewCurrency(declared.Currency)
	if err != nil {
		return market.Market{}, ReasonInvalidValue, err
	}
	cellID, err := cellvo.NewCellID(declared.CellID)
	if err != nil {
		return market.Market{}, ReasonInvalidValue, err
	}
	return market.New(id, name, currency, timeZone, cellID), "", nil
}
