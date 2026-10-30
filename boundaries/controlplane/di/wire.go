//go:build wireinject

package di

import (
	"github.com/google/wire"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	topologydi "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/di"
)

// Provider sets процесса controlplane объявлены здесь, под тегом wireinject: их
// использует только генератор. Инфраструктура и use cases слайса — в
// slices/topology/di; здесь — Kubernetes API и App. wire_gen.go генерирует
// `make generate`.

// configSet раскрывает секции конфигурации для provider'ов, которым нужна только своя секция.
var configSet = wire.FieldsOf(new(config.Config), "Shutdown", "Routes")

// InitializeApp — injector cmd/controlplane.
func InitializeApp(cfg config.Config) (*App, error) {
	wire.Build(configSet, NewRESTConfig, topologydi.ControllerSet, NewApp)
	return nil, nil
}
