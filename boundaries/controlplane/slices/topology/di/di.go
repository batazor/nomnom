// Package di описывает зависимости слайса topology для Google Wire (ADR 5):
// реестр на ресурсах Cell и Market, применение маршрутов публичного gateway и цикл
// reconcile, который их связывает.
package di

import (
	"fmt"

	"github.com/google/wire"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/controller"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/gatewayroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/registry"
)

// ControllerSet — слайс topology процесса cmd/controlplane.
var ControllerSet = wire.NewSet(NewManager, NewRouteApplier, NewModule)

// Module — собранный цикл reconcile для старта процесса.
type Module struct {
	Manager manager.Manager
	// Published закрывается после первой успешной публикации маршрутов.
	Published <-chan struct{}
}

// NewManager создаёт manager контроллера; без ROUTES__* процесс не стартует.
func NewManager(restConfig *rest.Config, routes config.RoutesConfig, shutdown config.ShutdownConfig) (manager.Manager, error) {
	if err := routes.Validate(); err != nil {
		return nil, err
	}
	return controller.NewManager(restConfig, routes.Namespace, shutdown.DrainTimeout)
}

// NewRouteApplier применяет маршруты публичного gateway через Kubernetes API.
func NewRouteApplier(restConfig *rest.Config, cfg config.RoutesConfig) (publishroutes.Applier, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	applier, err := gatewayroutes.NewForConfig(restConfig, gatewayroutes.Config{Namespace: cfg.Namespace, Gateway: cfg.Gateway, Listener: cfg.Listener})
	if err != nil {
		return nil, fmt.Errorf("routes: %w", err)
	}
	return applier, nil
}

// NewModule регистрирует цикл reconcile в manager.
func NewModule(mgr manager.Manager, applier publishroutes.Applier) (*Module, error) {
	published, err := controller.Register(mgr, publishroutes.New(registry.New(mgr.GetClient()), applier))
	if err != nil {
		return nil, err
	}
	return &Module{Manager: mgr, Published: published}, nil
}
