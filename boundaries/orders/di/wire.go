//go:build wireinject

package di

import (
	"github.com/google/wire"

	grpcadapter "nomnom/orders/infrastructure/grpc"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/messaging"
	fulfillmentdi "nomnom/orders/slices/fulfillment/di"
	orderdi "nomnom/orders/slices/order/di"
	simulatordi "nomnom/orders/slices/simulator/di"
)

// Provider sets процессов orders объявлены здесь, под тегом wireinject: их
// использует только генератор. Инфраструктура слайса (PostgreSQL, SpiceDB
// ports, телеметрия use cases) и его use cases, в том числе для Activities, — в
// slices/order/di, Activities Order Fulfillment — в slices/fulfillment/di; здесь — клиенты и транспорт
// процесса. Запуск и остановка ресурсов, которым нужен
// контекст Start или pipeline метрик процесса (Temporal client consumer'а и
// worker'а, Kafka, Watermill Router, telemetry.Start), остаются в lifecycle.Worker
// процесса, а не в Wire.
var (
	// configSet раскрывает секции конфигурации для provider'ов, которым нужна
	// только своя секция.
	configSet = wire.NewSet(wire.FieldsOf(new(config.Config),
		"Server", "Identity", "SpiceDB", "Database", "Cancellation", "Catalog", "Temporal", "Fulfillment", "ScheduledOrder", "InternalGateway", "Simulator", "SchemaRegistry"))
	// temporalAPISet — lazy Temporal client API и порт Order Fulfillment Workflow.
	temporalAPISet = wire.NewSet(NewTemporalClient, NewFulfillmentWorkflow)
	// grpcSet — gRPC-сервер бизнес-порта и проверка Subject.
	grpcSet = wire.NewSet(grpcadapter.NewSubjectResolver, NewGRPCServer)
	// storeSet — store процесса на схеме, мигрированной наборами di/schema,
	// в ролях портов слайса order.
	storeSet = wire.NewSet(NewStore, orderdi.RepositorySet)
)

// Injectors процессов orders: каждый cmd/* вызывает свой и передаёт результат
// в pkg/lifecycle; store применяет миграции при сборке. wire_gen.go генерирует
// `make generate`.

func InitializeApp(cfg config.Config) (*App, func(), error) {
	wire.Build(configSet, storeSet, NewReasonCodes, orderdi.TelemetrySet, orderdi.APISet, NewDatabasePool, simulatordi.APISet, NewSimulatorRunner, temporalAPISet, grpcSet, NewApp)
	return nil, nil, nil
}

func InitializeWorker(cfg config.Config) (*TemporalWorker, func(), error) {
	wire.Build(configSet, storeSet, NewReasonCodes, orderdi.TelemetrySet, orderdi.FulfillmentSet, fulfillmentdi.WorkerSet, NewTemporalWorker)
	return nil, nil, nil
}

func InitializeConsumer(cfg config.Config) (*KafkaConsumer, func(), error) {
	wire.Build(configSet, storeSet, NewReasonCodes, orderdi.TelemetrySet, orderdi.ConsumerSet, NewSchemaRegistry, wire.Bind(new(messaging.SchemaSubjects), new(*messaging.SchemaRegistry)), NewKafkaConsumer)
	return nil, nil, nil
}
