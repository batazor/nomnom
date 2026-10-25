//go:build wireinject

package di

import (
	"github.com/google/wire"

	grpcadapter "nomnom/orders/infrastructure/grpc"
	"nomnom/orders/pkg/config"
	orderdi "nomnom/orders/slices/order/di"
)

func InitializeApp(cfg config.Config) (*App, func(), error) {
	wire.Build(
		wire.FieldsOf(new(config.Config), "Server", "Identity"),
		orderdi.ProviderSet,
		grpcadapter.NewSubjectResolver,
		NewGRPCServer,
		NewApp,
	)
	return nil, nil, nil
}

func InitializeWorker(cfg config.Config) (*TemporalWorker, func(), error) {
	wire.Build(
		orderdi.NewStore,
		NewActivities,
		NewTemporalWorker,
	)
	return nil, nil, nil
}
