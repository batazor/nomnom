// Package di описывает зависимости слайса topology для Google Wire (ADR 5):
// PostgreSQL-репозитории реестра, проверка SpiceDB и use cases Cell, Market и
// Placement Map.
package di

import (
	"fmt"

	"github.com/google/wire"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/createmarket"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/getplacementmap"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/listmarkets"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/registercell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/repository"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/spicedb"
)

// APISet — слайс topology процесса cmd/controlplane.
var APISet = wire.NewSet(NewAuthorizer, NewReaders, NewModule)

// Module — собранные use cases слайса для gRPC adapter.
type Module struct {
	RegisterCell    registercell.Handler
	CreateMarket    createmarket.Handler
	ListMarkets     listmarkets.Handler
	GetPlacementMap getplacementmap.Handler
}

func NewModule(store *repository.Store, authorizer application.Authorizer, readers application.ServiceAllowlist) *Module {
	markets := repository.NewMarkets(store)
	return &Module{
		RegisterCell:    registercell.New(authorizer, repository.NewCells(store)),
		CreateMarket:    createmarket.New(authorizer, markets),
		ListMarkets:     listmarkets.New(readers, markets),
		GetPlacementMap: getplacementmap.New(readers, repository.NewPlacements(store)),
	}
}

// NewAuthorizer подключает проверку platform#manage_markets к SpiceDB (ADR 18);
// без endpoint процесс не стартует.
func NewAuthorizer(cfg config.SpiceDBConfig) (application.Authorizer, func(), error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	client, err := spicedb.Dial(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("spicedb: %w", err)
	}
	return spicedb.NewAuthorizer(client, cfg.Timeout), func() { _ = client.Close() }, nil
}

// NewReaders — services, читающие Markets и Placement Map (IDENTITY__PLACEMENT_READERS).
func NewReaders(cfg config.IdentityConfig) application.ServiceAllowlist {
	return application.ServiceAllowlist(cfg.Readers())
}
