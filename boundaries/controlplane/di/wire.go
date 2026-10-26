//go:build wireinject

package di

import (
	"github.com/google/wire"

	grpcadapter "github.com/batazor/nomnom/boundaries/controlplane/infrastructure/grpc"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/config"
	topologydi "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/di"
)

// Provider sets процесса controlplane объявлены здесь, под тегом wireinject: их
// использует только генератор. Инфраструктура и use cases слайса — в
// slices/topology/di; здесь — store, транспорт и App. wire_gen.go генерирует
// `make generate`.
var (
	// configSet раскрывает секции конфигурации для provider'ов, которым нужна только своя секция.
	configSet = wire.FieldsOf(new(config.Config), "Server", "Identity", "SpiceDB", "Database", "Routes")
	// grpcSet — gRPC-сервер бизнес-порта и проверка Subject.
	grpcSet = wire.NewSet(grpcadapter.NewSubjectResolver, NewGRPCServer)
)

// InitializeApp — injector cmd/controlplane: store применяет миграции при сборке.
func InitializeApp(cfg config.Config) (*App, func(), error) {
	wire.Build(configSet, NewStore, topologydi.APISet, grpcSet, NewApp)
	return nil, nil, nil
}
