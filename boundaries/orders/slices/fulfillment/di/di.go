// Package di описывает зависимости слайса fulfillment для Google Wire.
// Доменная модель процесса и workflow Wire не импортируют.
package di

import (
	"log"
	"nomnom/orders/pkg/application"
	gatewayclient "nomnom/orders/pkg/gateway"

	"github.com/google/wire"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/fulfillment/infrastructure/neighbours"
	"nomnom/orders/slices/fulfillment/infrastructure/neighbours/grpcclient"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// WorkerSet — Activities fulfillment orders-worker и адаптеры соседних
// контекстов. Store процесса и телеметрию use cases даёт слайс order
// (PostgresSet, TelemetrySet); роль Store связывает composition root.
var WorkerSet = wire.NewSet(NewNeighbours, NewActivities)

// Store — роль хранилища Activities: чтение Order для запросов соседям и Unit
// of Work, repository и Outbox use cases Complete Order и Fail Or Cancel Order.
type Store interface {
	application.UnitOfWork
	order.Repository
	completeorder.Outbox
}

// Neighbours — адаптеры payments, restaurants и delivery для Activities
// fulfillment.
type Neighbours struct {
	Payments    fulfillmenttemporal.Payments
	Restaurants fulfillmenttemporal.Restaurants
	Delivery    fulfillmenttemporal.Delivery
}

// NewNeighbours выбирает адаптеры соседних контекстов по fulfillment.neighbours;
// cleanup закрывает соединение с internal gateway.
func NewNeighbours(cfg config.FulfillmentConfig, gateway config.InternalGatewayConfig, identity config.IdentityConfig) (Neighbours, func(), error) {
	switch cfg.Neighbours {
	case config.NeighboursGRPC:
		conn, err := gatewayclient.Dial(gateway.Address, identity.TokenDir)
		if err != nil {
			return Neighbours{}, nil, err
		}
		return Neighbours{
			Payments:    grpcclient.NewPayments(conn),
			Restaurants: grpcclient.NewRestaurants(conn),
			Delivery:    grpcclient.NewDelivery(conn),
		}, func() { _ = conn.Close() }, nil
	case config.NeighboursSimulated:
		log.Printf("WARNING: fulfillment neighbours are simulated: payments, restaurants and delivery calls are approved without effect")
		return Neighbours{Payments: neighbours.Simulated{}, Restaurants: neighbours.Simulated{}, Delivery: neighbours.Simulated{}}, func() {}, nil
	default:
		return Neighbours{Payments: neighbours.Unavailable{}, Restaurants: neighbours.Unavailable{}, Delivery: neighbours.Unavailable{}}, func() {}, nil
	}
}

// NewActivities собирает Activities fulfillment поверх store слайса order и
// адаптеров соседних контекстов. Workflow-код зависимостей не получает: внешние
// операции выполняются только этими Activities (ADR 5).
func NewActivities(store Store, neighbours Neighbours, uc *telemetry.UseCases) *fulfillmenttemporal.Activities {
	return &fulfillmenttemporal.Activities{
		Orders:      store,
		Payments:    neighbours.Payments,
		Restaurants: neighbours.Restaurants,
		Delivery:    neighbours.Delivery,
		Complete:    telemetry.Instrument(uc, observability.CompleteOrder, completeorder.NewHandler(store, store, store).Handle),
		Finalize:    telemetry.Instrument(uc, observability.FailOrCancelOrder, failorcancelorder.NewHandler(store, store, store).Handle),
	}
}
