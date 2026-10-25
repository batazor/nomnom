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
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recordcourierrelease"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"nomnom/orders/slices/order/infrastructure/catalog"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/postgres"
	"nomnom/orders/slices/order/infrastructure/spicedb"
	"nomnom/orders/slices/order/infrastructure/system"
)

// Provider sets слайса order. Composition root процесса (корневой di) выбирает
// нужные: инфраструктура — PostgresSet и TelemetrySet, use cases процесса —
// APISet или ConsumerSet; Activities orders-worker собирает слайс fulfillment
// (slices/fulfillment/di) поверх PostgresSet и TelemetrySet. Новый порт
// добавляется полем в структуру зависимостей набора и provider'ом; позиционных
// конструкторов нет.
var (
	// PostgresSet — общий PostgreSQL store процесса (ADR 8) во всех ролях портов:
	// ProcessStore — PostgreSQL с Outbox из messaging/outbound, Unit of Work и
	// Outbox под декоратором бизнес-метрик (observability.ObservedStore).
	PostgresSet = wire.NewSet(NewPostgresStore, observability.NewDomainMetrics, NewProcessStore,
		wire.Bind(new(PlacementStore), new(*ProcessStore)),
		wire.Bind(new(ReadModels), new(*ProcessStore)),
		wire.Bind(new(InboxStore), new(*ProcessStore)),
		wire.Bind(new(spicedb.RevisionReader), new(*ProcessStore)))
	// TelemetrySet — инструменты телеметрии use cases, общие для handlers процесса.
	TelemetrySet = wire.NewSet(observability.UseCases)
	// APISet — command и query use cases cmd/orders и их порты, кроме Workflow:
	// Temporal client выбирает composition root.
	APISet = wire.NewSet(NewAuthorizer, NewCancellationPolicy, NewCatalog, NewClock, NewOrderIDs,
		wire.Struct(new(Dependencies), "*"), NewModule)
	// ConsumerSet — handlers orders-consumer без Notifier workflow: его Temporal
	// client открывает KafkaConsumer.Start.
	ConsumerSet = wire.NewSet(NewCourierRelationships, wire.Struct(new(Consumer), "*"))
)

// Authorizer — permission checks use cases и creation-time relationships слайса.
type Authorizer interface {
	Check(ctx context.Context, check application.AccessCheck) error
	GrantOrderRelationships(ctx context.Context, orderID order.ID, customerID order.CustomerID, restaurantID order.RestaurantID) (application.AuthorizationRevision, error)
}

// CancellationPolicy — окно отмены, общее для Get Order и Request Order
// Cancellation.
type CancellationPolicy = getorder.CancellationPolicy

// NewCancellationPolicy строит production-политику из конфигурации; тестовые
// тестовая fake.AlwaysOpen сюда не попадает (FR-462).
func NewCancellationPolicy(cfg config.CancellationConfig) (CancellationPolicy, error) {
	policy, err := cancellationpolicy.NewFixed(cfg.Window)
	if err != nil {
		return nil, err
	}
	return cancellationpolicy.Observe(policy, slog.Default()), nil
}

// Workflow — Order Fulfillment Workflow для command use cases API: Place Order
// запускает его, Request Order Cancellation передаёт ему запрос.
type Workflow interface {
	fulfillment.Starter
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

// NewClock — системные часы use cases.
func NewClock() application.Clock { return system.Clock{} }

// NewOrderIDs — генератор UUIDv7 идентификаторов Order.
func NewOrderIDs() placeorder.IDGenerator { return system.OrderIDs{} }

// NewPostgresStore — общий store API, worker'а и consumer'а;
// без POSTGRES__DSN процесс не стартует. Сначала процесс приводит схему к
// встроенным миграциям: применённые golang-migrate пропускает, недостающие
// применяет под advisory lock, так что одновременный старт pods сериализуется. Ошибка
// миграции останавливает процесс до запуска остального. Pool подключается лениво.
func NewPostgresStore(cfg config.PostgresConfig) (*postgres.Store, func(), error) {
	if err := postgres.Migrate(context.Background(), cfg); err != nil {
		return nil, nil, err
	}
	store, err := postgres.Open(context.Background(), cfg)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}

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

// CourierRelationships — writer order#delivery процесса orders-consumer:
// назначение (Record Courier Assignment) и отмена Delivery (Record Courier
// Release). Доступ курьера к заказу отзывает delivery (delivery#courier).
type CourierRelationships interface {
	recordcourierassignment.Relationships
	recordcourierrelease.Relationships
}

// NewCourierRelationships подключает consumer к SpiceDB: order#delivery пишется
// в Inbox unit of work до commit, поэтому без SPICEDB__ENDPOINT процесс не стартует.
func NewCourierRelationships(cfg config.SpiceDBConfig) (CourierRelationships, func(), error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil, fmt.Errorf("%s is required: orders-consumer writes order#delivery to SpiceDB", config.EnvSpiceDBEndpoint)
	}
	client, err := spicedb.Dial(cfg)
	if err != nil {
		return nil, nil, err
	}
	return spicedb.NewAuthorizer(client, nil, cfg.Timeout), func() { _ = client.Close() }, nil
}
