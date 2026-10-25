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
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recordcourierrelease"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"nomnom/orders/slices/order/infrastructure/catalog"
	"nomnom/orders/slices/order/infrastructure/neighbours"
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
	// PostgresSet — общий PostgreSQL store процесса (ADR 8) и его роль портов Store.
	// Store процесса — PostgreSQL под декоратором бизнес-метрик (ObservedStore).
	PostgresSet = wire.NewSet(NewPostgresStore, observability.NewDomainMetrics, NewObservedStore)
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

// Store — persistence-порты слайса: Unit of Work, repository, Inbox, Outbox,
// idempotency keys, read models и authorization revision. В процессах — общий
// PostgreSQL (ADR 8), в unit-тестах — in-memory store.
type Store interface {
	application.UnitOfWork
	application.Inbox
	order.Repository
	Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error
	FindOrder(ctx context.Context, customerID order.CustomerID, key string) (order.ID, bool, error)
	RememberOrder(ctx context.Context, customerID order.CustomerID, key string, orderID order.ID) error
	RememberRevision(ctx context.Context, orderID order.ID, revision application.AuthorizationRevision) error
	Revision(ctx context.Context, orderID string) (application.AuthorizationRevision, bool, error)
	recordcourierassignment.Assignments
	OrderView(ctx context.Context, id order.ID) (getorder.OrderView, error)
	ListCustomerOrders(ctx context.Context, request listcustomerorders.PageRequest) (listcustomerorders.Page, error)
	CountByState(ctx context.Context) (map[order.State]int64, error)
}

// ObservedStore — Store, который после commit внешнего Unit of Work записывает
// бизнес-метрики по Domain Events, попавшим в Outbox (observability.DomainMetrics).
type ObservedStore struct {
	Store
	Metrics *observability.DomainMetrics
}

// NewObservedStore — Store процессов поверх PostgreSQL.
func NewObservedStore(store *postgres.Store, metrics *observability.DomainMetrics) Store {
	return ObservedStore{Store: store, Metrics: metrics}
}

func (s ObservedStore) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	ctx, done := s.Metrics.Track(ctx)
	err := s.Store.Do(ctx, fn)
	done(err)
	return err
}

func (s ObservedStore) Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error {
	if err := s.Store.Append(ctx, aggregateID, aggregateVersion, events); err != nil {
		return err
	}
	s.Metrics.Appended(ctx, events)
	return nil
}

// Authorizer — permission checks use cases и creation-time relationships слайса.
type Authorizer interface {
	Check(ctx context.Context, check application.AccessCheck) error
	GrantOrderRelationships(ctx context.Context, orderID order.ID, customerID order.CustomerID, restaurantID order.RestaurantID) (application.AuthorizationRevision, error)
}

// CancellationPolicy — окно отмены, общее для Get Order и Request Order
// Cancellation.
type CancellationPolicy = getorder.CancellationPolicy

// NewCancellationPolicy строит production-политику из конфигурации; тестовые
// memory.AlwaysOpen и memory.FixedWindow сюда не попадают (FR-462).
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

// NewCatalog — catalog snapshot Place Order, выбранный тем же
// fulfillment.neighbours, что и адаптеры Activities. grpc (по умолчанию) читает
// GetRestaurant и GetMenu restaurants через internal gateway (lazy connection,
// projected token из IDENTITY__TOKEN_DIR, если задан); unavailable и simulated
// оставляют fail-closed catalog: цены, которые orders не может проверить, не
// симулируются, и Place Order отвечает UNAVAILABLE.
func NewCatalog(cfg config.FulfillmentConfig, gateway config.InternalGatewayConfig, identity config.IdentityConfig, limits config.CatalogConfig) (placeorder.Catalog, func(), error) {
	if cfg.Neighbours != config.NeighboursGRPC {
		if cfg.Neighbours == config.NeighboursSimulated {
			slog.Warn("restaurants catalog is not simulated: Place Order answers UNAVAILABLE", "neighbours", cfg.Neighbours)
		}
		return neighbours.UnavailableCatalog{}, func() {}, nil
	}
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

// NewPostgresStore — общий store API, worker'а, consumer'а и forwarder'а;
// без POSTGRES__DSN процесс не стартует. Pool подключается лениво.
func NewPostgresStore(cfg config.PostgresConfig) (*postgres.Store, func(), error) {
	store, err := postgres.Open(context.Background(), cfg)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}

// NewAuthorizer connects the authorization ports to SpiceDB (ADR 18). The
// endpoint is required: a process that checks permissions does not start
// without it. Checks of an Order read at least as fresh as its stored revision.
func NewAuthorizer(cfg config.SpiceDBConfig, store Store) (Authorizer, func(), error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil, fmt.Errorf("%s is required: orders checks permissions in SpiceDB", config.EnvSpiceDBEndpoint)
	}
	client, err := spicedb.Dial(cfg)
	if err != nil {
		return nil, nil, err
	}
	return spicedb.NewAuthorizer(client, store, cfg.Timeout), func() { _ = client.Close() }, nil
}

// CourierRelationships — writer order#courier и order#delivery процесса
// orders-consumer: назначение (Record Courier Assignment) и снятие без замены
// или отмена Delivery (Record Courier Release).
type CourierRelationships interface {
	recordcourierassignment.Relationships
	recordcourierrelease.Relationships
}

// NewCourierRelationships подключает consumer к SpiceDB: связи курьера пишутся
// в Inbox unit of work до commit, поэтому без SPICEDB__ENDPOINT процесс не стартует.
func NewCourierRelationships(cfg config.SpiceDBConfig) (CourierRelationships, func(), error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil, fmt.Errorf("%s is required: orders-consumer writes courier relationships to SpiceDB", config.EnvSpiceDBEndpoint)
	}
	client, err := spicedb.Dial(cfg)
	if err != nil {
		return nil, nil, err
	}
	return spicedb.NewAuthorizer(client, nil, cfg.Timeout), func() { _ = client.Close() }, nil
}
