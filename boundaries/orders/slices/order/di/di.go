// Package di описывает зависимости слайса order для Google Wire.
// Доменная модель и handlers use cases Wire не импортируют.
package di

import (
	"context"
	"fmt"
	"log/slog"
	gatewayclient "nomnom/orders/pkg/gateway"
	"strings"

	"github.com/google/wire"
	"github.com/shopspring/decimal"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"nomnom/orders/slices/order/infrastructure/catalog"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/scheduledslots"
	"nomnom/orders/slices/order/infrastructure/spicedb"
	"nomnom/orders/slices/order/infrastructure/system"
)

// Provider sets слайса order. Composition root процесса (корневой di) выбирает
// нужные: инфраструктура — RepositorySet и TelemetrySet, use cases процесса —
// APISet, ConsumerSet или FulfillmentSet; Activities orders-worker собирает слайс
// fulfillment (slices/fulfillment/di) поверх FulfillmentSet. Новый порт
// добавляется полем в структуру зависимостей набора и provider'ом; позиционных
// конструкторов нет.
var (
	// RepositorySet — общий PostgreSQL store процесса (ADR 8) во всех ролях портов:
	// ProcessStore — PostgreSQL с Outbox из messaging/outbound, Unit of Work и
	// Outbox под декоратором бизнес-метрик (observability.ObservedStore).
	// *repository.Store на мигрированной схеме даёт composition root: он применяет
	// наборы миграций всех слайсов (di/schema).
	RepositorySet = wire.NewSet(observability.NewDomainMetrics, NewProcessStore,
		wire.Bind(new(PlacementStore), new(*ProcessStore)),
		wire.Bind(new(ReadModels), new(*ProcessStore)),
		wire.Bind(new(InboxStore), new(*ProcessStore)),
		wire.Bind(new(FulfillmentStore), new(*ProcessStore)),
		wire.Bind(new(spicedb.RevisionReader), new(*ProcessStore)))
	// TelemetrySet — инструменты телеметрии use cases, общие для handlers процесса.
	TelemetrySet = wire.NewSet(observability.UseCases)
	// APISet — command и query use cases cmd/orders и их порты, кроме Workflow:
	// Temporal client выбирает composition root.
	APISet = wire.NewSet(NewAuthorizer, NewCancellationPolicy, NewCatalog, NewSlotReservations, NewClock, NewOrderIDs,
		wire.Struct(new(Dependencies), "*"), NewModule)
	// ConsumerSet — handlers orders-consumer без Notifier workflow: его Temporal
	// client открывает KafkaConsumer.Start.
	ConsumerSet = wire.NewSet(NewRelationshipSync, wire.Struct(new(Consumer), "*"))
	// FulfillmentSet — use cases, которые вызывают Activities orders-worker.
	FulfillmentSet = wire.NewSet(NewFulfillmentEstimates, wire.Struct(new(FulfillmentDependencies), "*"), NewFulfillmentModule)
)

// Authorizer — permission checks use cases слайса. Связи Order API не пишет:
// их синхронизирует orders-consumer (ADR 18).
type Authorizer interface {
	Check(ctx context.Context, check application.AccessCheck) error
}

// CancellationPolicy — окно отмены, общее для Get Order и Request Order
// Cancellation.
type CancellationPolicy = orderdetails.CancellationPolicy

// NewCancellationPolicy строит production-политику из конфигурации; тестовые
// тестовая fake.AlwaysOpen сюда не попадает (FR-462).
func NewCancellationPolicy(cfg config.CancellationConfig) (CancellationPolicy, error) {
	policy, err := cancellationpolicy.NewFixed(cfg.Window, cfg.ScheduledCutoff)
	if err != nil {
		return nil, err
	}
	return cancellationpolicy.Observe(policy, slog.Default()), nil
}

// Workflow — Order Fulfillment Workflow для command use cases API: Request
// Order Cancellation передаёт ему запрос. Запускает workflow consumer
// собственного OrderPlaced (startfulfillment).
type Workflow interface {
	fulfillment.CancellationRequester
}

// NewCatalog — catalog snapshot Place Order: GetRestaurant и GetMenu restaurants
// через internal gateway (lazy connection, projected token из IDENTITY__TOKEN_DIR).
func NewCatalog(gateway config.InternalGatewayConfig, identity config.IdentityConfig, limits config.CatalogConfig) (placeorder.Catalog, func(), error) {
	minimum, err := decimal.NewFromString(limits.MinimumTotal)
	if err != nil {
		return nil, nil, fmt.Errorf("catalog minimum_total: %w", err)
	}
	maximum, err := decimal.NewFromString(limits.MaximumTotal)
	if err != nil {
		return nil, nil, fmt.Errorf("catalog maximum_total: %w", err)
	}
	conn, err := gatewayclient.Dial(gateway.Address, identity.TokenDir)
	if err != nil {
		return nil, nil, err
	}
	adapter := catalog.New(conn, catalog.Limits{MinimumTotal: minimum, MaximumTotal: maximum}, limits.Timeout)
	return adapter, func() { _ = conn.Close() }, nil
}

// NewSlotReservations — резервация окна запланированного заказа Place Order:
// ReserveScheduledSlot restaurants через internal gateway от service identity
// orders (projected token nomnom.restaurants из IDENTITY__TOKEN_DIR); deadline —
// CATALOG__TIMEOUT, как у чтений каталога до транзакции.
func NewSlotReservations(gateway config.InternalGatewayConfig, identity config.IdentityConfig, catalog config.CatalogConfig) (placeorder.SlotReservations, func(), error) {
	conn, err := gatewayclient.Dial(gateway.Address, identity.TokenDir)
	if err != nil {
		return nil, nil, err
	}
	return scheduledslots.New(conn, catalog.Timeout), func() { _ = conn.Close() }, nil
}

// NewClock — системные часы use cases.
func NewClock() application.Clock { return system.Clock{} }

// NewOrderIDs — генератор UUIDv7 идентификаторов Order.
func NewOrderIDs() placeorder.IDGenerator { return system.OrderIDs{} }

// NewAuthorizer connects the authorization ports to SpiceDB (ADR 18). The
// endpoint is required: a process that checks permissions does not start
// without it. Checks of an Order read at least as fresh as its stored revision.
func NewAuthorizer(cfg config.SpiceDBConfig, revisions spicedb.RevisionReader) (Authorizer, func(), error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil, fmt.Errorf("%s is required: orders checks permissions in SpiceDB", config.EnvSpiceDBEndpoint)
	}
	client, err := spicedb.Dial(cfg)
	if err != nil {
		return nil, nil, err
	}
	return spicedb.NewAuthorizer(client, revisions, cfg.Timeout), func() { _ = client.Close() }, nil
}

// RelationshipSync — writer связей order в SpiceDB процесса orders-consumer:
// Sync Order Relationships приводит order#customer, order#restaurant и
// order#delivery к состоянию Order по RelationshipsChanged (ADR 18).
type RelationshipSync = syncorderrelationships.Relationships

// NewRelationshipSync подключает consumer к SpiceDB: без SPICEDB__ENDPOINT
// процесс не стартует.
func NewRelationshipSync(cfg config.SpiceDBConfig) (RelationshipSync, func(), error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil, fmt.Errorf("%s is required: orders-consumer syncs order relationships to SpiceDB", config.EnvSpiceDBEndpoint)
	}
	client, err := spicedb.Dial(cfg)
	if err != nil {
		return nil, nil, err
	}
	return spicedb.NewAuthorizer(client, nil, cfg.Timeout), func() { _ = client.Close() }, nil
}
