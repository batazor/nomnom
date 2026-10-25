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
		wire.FieldsOf(new(config.Config), "Server", "Identity", "SpiceDB", "Postgres", "Cancellation"),
		orderdi.ProviderSet,
		grpcadapter.NewSubjectResolver,
		NewGRPCServer,
		NewApp,
	)
	return nil, nil, nil
}

func InitializeWorker(cfg config.Config) (*TemporalWorker, func(), error) {
	wire.Build(
		wire.FieldsOf(new(config.Config), "Postgres"),
		orderdi.NewPostgresStore,
		orderdi.NewStore,
		NewActivities,
		NewTemporalWorker,
	)
	return nil, nil, nil
}

func InitializeConsumer(cfg config.Config) (*KafkaConsumer, func(), error) {
	wire.Build(
		wire.FieldsOf(new(config.Config), "Postgres"),
		orderdi.NewPostgresStore,
		orderdi.NewStore,
		NewKafkaConsumer,
	)
	return nil, nil, nil
}

func InitializeForwarder(cfg config.Config) (*OutboxForwarder, func(), error) {
	wire.Build(
		wire.FieldsOf(new(config.Config), "Postgres"),
		orderdi.NewPostgresStore,
		NewOutboxForwarder,
	)
	return nil, nil, nil
}

func InitializeReconciler(cfg config.Config) (*RelationshipReconciler, func(), error) {
	wire.Build(
		wire.FieldsOf(new(config.Config), "Postgres", "SpiceDB"),
		orderdi.NewPostgresStore,
		NewSpiceDBClient,
		NewRelationshipReconciler,
	)
	return nil, nil, nil
}
