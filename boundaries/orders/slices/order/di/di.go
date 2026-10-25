// Package di описывает зависимости слайса order для Google Wire.
// Доменная модель и handlers use cases Wire не импортируют.
package di

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/wire"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/cancellationpolicy"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/postgres"
	"nomnom/orders/slices/order/infrastructure/spicedb"
)

// ProviderSet собирает компоненты слайса order. Provider'ы handlers
// добавляются сюда вместе с реализацией соответствующих use cases.
var ProviderSet = wire.NewSet(NewPostgresStore, NewStore, NewAuthorizer, NewCancellationPolicy, NewModule)

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
	OrderView(ctx context.Context, id order.ID) (getorder.OrderView, error)
	ListCustomerOrders(ctx context.Context, request listcustomerorders.PageRequest) (listcustomerorders.Page, error)
}

// Authorizer — permission checks use cases и creation-time relationships слайса.
type Authorizer interface {
	Check(ctx context.Context, check application.AccessCheck) error
	GrantOrderCustomer(ctx context.Context, orderID order.ID, customerID order.CustomerID) (application.AuthorizationRevision, error)
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

// Module — собранный слайс order, который получает composition root процесса.
type Module struct {
	GetOrder           application.Handler[getorder.Query, getorder.OrderView]
	ListCustomerOrders application.Handler[listcustomerorders.Query, listcustomerorders.Page]
}

// NewPostgresStore — общий store API, worker'а, consumer'а и forwarder'а;
// без POSTGRES__DSN процесс не стартует. Pool подключается лениво.
func NewPostgresStore(cfg config.PostgresConfig) (*postgres.Store, func(), error) {
	store, err := postgres.Open(context.Background(), cfg)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}

// NewStore отдаёт PostgreSQL store как порты слайса.
func NewStore(store *postgres.Store) Store { return store }

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

// NewModule собирает handlers под декоратором телеметрии use cases.
func NewModule(store Store, authorizer Authorizer, policy CancellationPolicy) *Module {
	uc := observability.UseCases()
	return &Module{
		GetOrder:           telemetry.Instrument(uc, observability.GetOrder, getorder.NewHandler(authorizer, store, policy).Handle),
		ListCustomerOrders: telemetry.Instrument(uc, observability.ListCustomerOrders, listcustomerorders.NewHandler(authorizer, store).Handle),
	}
}
