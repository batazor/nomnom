// Package di описывает зависимости слайса topology для Google Wire (ADR 5):
// PostgreSQL-репозитории реестра, проверка SpiceDB, публикация маршрутов
// публичного gateway и use cases Cell и Market.
package di

import (
	"fmt"

	"github.com/google/wire"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/createmarket"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/listmarkets"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/registercell"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/gatewayroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/repository"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/spicedb"
)

// APISet — слайс topology процесса cmd/controlplane.
var APISet = wire.NewSet(NewAuthorizer, NewRouteApplier, NewModule)

// Module — собранные use cases слайса для gRPC adapter и старта процесса.
type Module struct {
	RegisterCell  registercell.Handler
	CreateMarket  createmarket.Handler
	ListMarkets   listmarkets.Handler
	PublishRoutes publishroutes.Handler
}

func NewModule(store *repository.Store, authorizer application.Authorizer, applier publishroutes.Applier) *Module {
	markets := repository.NewMarkets(store)
	routes := publishroutes.New(repository.NewRoutes(store), applier)
	return &Module{
		RegisterCell:  registercell.New(authorizer, repository.NewCells(store), routes),
		CreateMarket:  createmarket.New(authorizer, markets, routes),
		ListMarkets:   listmarkets.New(authorizer, markets),
		PublishRoutes: routes,
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

// NewRouteApplier применяет маршруты публичного gateway через Kubernetes API
// кластера (in-cluster ServiceAccount); без ROUTES__* процесс не стартует.
func NewRouteApplier(cfg config.RoutesConfig) (publishroutes.Applier, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	applier, err := gatewayroutes.NewInCluster(gatewayroutes.Config{Namespace: cfg.Namespace, Gateway: cfg.Gateway, Listener: cfg.Listener})
	if err != nil {
		return nil, fmt.Errorf("routes: %w", err)
	}
	return applier, nil
}
