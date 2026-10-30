// Package di описывает зависимости слайса simulator для Google Wire: switch
// Simulator (FR-604) и демо-покупатель (FR-605) поверх use cases слайса order.
package di

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/google/wire"
	"github.com/jackc/pgx/v5/pgxpool"

	"nomnom/orders/pkg/config"
	gatewayclient "nomnom/orders/pkg/gateway"
	orderdi "nomnom/orders/slices/order/di"
	"nomnom/orders/slices/order/domains/order/vo"
	"nomnom/orders/slices/simulator/applications/customersimulator"
	"nomnom/orders/slices/simulator/applications/switchsimulator"
	simulatorrepository "nomnom/orders/slices/simulator/infrastructure/repository"
	"nomnom/orders/slices/simulator/infrastructure/restaurants"
)

// APISet — слайс simulator процесса cmd/orders.
var APISet = wire.NewSet(NewModule)

// Module — собранный слайс: handlers switch для gRPC и покупатель для runner'а
// процесса, который вызывает Customer.Tick каждые Interval.
type Module struct {
	Switch   switchsimulator.Handlers
	Customer *customersimulator.Customer
	Interval time.Duration
}

// NewModule проверяет секцию simulator (без неё cmd/orders не стартует,
// AC-765) и открывает lazy connection к restaurants через internal gateway.
func NewModule(cfg config.SimulatorConfig, gateway config.InternalGatewayConfig, identity config.IdentityConfig,
	catalog config.CatalogConfig, pool *pgxpool.Pool, authorizer orderdi.Authorizer, order *orderdi.Module,
) (*Module, func(), error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	markets := make([]vo.MarketID, 0, len(cfg.Markets()))
	for _, raw := range cfg.Markets() {
		market, err := vo.NewMarketID(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("simulator config: %w", err)
		}
		markets = append(markets, market)
	}
	conn, err := gatewayclient.Dial(gateway.Address, identity.TokenDir)
	if err != nil {
		return nil, nil, fmt.Errorf("simulator catalog: %w", err)
	}
	state := simulatorrepository.NewState(pool)
	customer := customersimulator.New(customersimulator.Dependencies{
		Switch:              state,
		Catalog:             restaurants.New(conn, markets, catalog.Timeout),
		PlaceOrder:          order.PlaceOrder,
		GetOrder:            order.GetOrder,
		RequestCancellation: order.RequestOrderCancellation,
		//nolint:gosec // simulated choices, not security-sensitive.
		Random:     rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		Keys:       uuidKeys{},
		CustomerID: cfg.CustomerID,
		MinDelay:   cfg.MinDelay,
		MaxDelay:   cfg.MaxDelay,
	})
	return &Module{
		Switch:   switchsimulator.NewHandlers(authorizer, state),
		Customer: customer,
		Interval: cfg.Interval,
	}, func() { _ = conn.Close() }, nil
}

type uuidKeys struct{}

func (uuidKeys) NewKey() string { return uuid.NewString() }
