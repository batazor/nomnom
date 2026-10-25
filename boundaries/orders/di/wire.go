//go:build wireinject

package di

import (
	"github.com/google/wire"

	grpcadapter "nomnom/orders/infrastructure/grpc"
	"nomnom/orders/pkg/config"
	orderdi "nomnom/orders/slices/order/di"
)

// Provider sets процессов orders объявлены здесь, под тегом wireinject: их
// использует только генератор. Инфраструктура слайса (PostgreSQL, SpiceDB
// ports, телеметрия use cases) и его use cases — в slices/order/di; здесь —
// клиенты и транспорт процесса. Запуск и остановка ресурсов, которым нужен
// контекст Start или pipeline метрик процесса (Temporal client consumer'а и
// worker'а, Kafka, Watermill Router, telemetry.Start), остаются в lifecycle.Worker
// процесса, а не в Wire.
var (
	// configSet раскрывает секции конфигурации для provider'ов, которым нужна
	// только своя секция.
	configSet = wire.NewSet(wire.FieldsOf(new(config.Config),
		"Server", "Identity", "SpiceDB", "Postgres", "Cancellation", "Temporal", "Fulfillment", "InternalGateway"))
	// temporalAPISet — lazy Temporal client API и порт Order Fulfillment Workflow.
	temporalAPISet = wire.NewSet(NewTemporalClient, NewFulfillmentWorkflow)
	// grpcSet — gRPC-сервер бизнес-порта и проверка Subject.
	grpcSet = wire.NewSet(grpcadapter.NewSubjectResolver, NewGRPCServer)
	// spiceDBSet — клиент SpiceDB процесса orders-reconciler.
	spiceDBSet = wire.NewSet(NewSpiceDBClient)
)

// Injectors процессов orders: каждый cmd/* вызывает свой и передаёт результат
// в pkg/lifecycle (orders-migrate — запускает Run). wire_gen.go генерирует
// `make generate`.

func InitializeApp(cfg config.Config) (*App, func(), error) {
	wire.Build(configSet, orderdi.PostgresSet, orderdi.TelemetrySet, orderdi.APISet, temporalAPISet, grpcSet, NewApp)
	return nil, nil, nil
}

func InitializeWorker(cfg config.Config) (*TemporalWorker, func(), error) {
	wire.Build(configSet, orderdi.PostgresSet, orderdi.TelemetrySet, orderdi.WorkerSet, NewTemporalWorker)
	return nil, nil, nil
}

func InitializeConsumer(cfg config.Config) (*KafkaConsumer, func(), error) {
	wire.Build(configSet, orderdi.PostgresSet, orderdi.TelemetrySet, orderdi.ConsumerSet, NewKafkaConsumer)
	return nil, nil, nil
}

func InitializeForwarder(cfg config.Config) (*OutboxForwarder, func(), error) {
	wire.Build(configSet, orderdi.PostgresSet, NewOutboxForwarder)
	return nil, nil, nil
}

func InitializeReconciler(cfg config.Config) (*RelationshipReconciler, func(), error) {
	wire.Build(configSet, orderdi.PostgresSet, spiceDBSet, NewRelationshipReconciler)
	return nil, nil, nil
}

func InitializeMigrator(cfg config.Config) *Migrator {
	wire.Build(configSet, NewMigrator)
	return nil
}
