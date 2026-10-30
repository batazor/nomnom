// Package di описывает зависимости слайса fulfillment для Google Wire.
// Доменная модель процесса и workflow Wire не импортируют.
package di

import (
	gatewayclient "nomnom/orders/infrastructure/gateway"
	ports "nomnom/orders/slices/fulfillment/applications/neighbours"
	"nomnom/orders/slices/fulfillment/applications/orders"

	"github.com/google/wire"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/fulfillment/infrastructure/neighbours/grpcclient"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
)

// WorkerSet — Activities fulfillment orders-worker и адаптеры соседних
// контекстов. Порт orders.Orders даёт composition root поверх use cases слайса
// order: слайсы друг друга не импортируют.
var WorkerSet = wire.NewSet(NewNeighbours, NewActivities)

// Neighbours — адаптеры payments, restaurants и delivery для Activities
// fulfillment.
type Neighbours struct {
	Payments    ports.Payments
	Restaurants ports.Restaurants
	Delivery    ports.Delivery
}

// NewNeighbours — gRPC-клиенты payments, restaurants и delivery через internal
// gateway; cleanup закрывает соединение.
func NewNeighbours(gateway config.InternalGatewayConfig, identity config.IdentityConfig) (Neighbours, func(), error) {
	conn, err := gatewayclient.Dial(gateway.Address, identity.TokenDir)
	if err != nil {
		return Neighbours{}, nil, err
	}
	return Neighbours{
		Payments:    grpcclient.NewPayments(conn),
		Restaurants: grpcclient.NewRestaurants(conn),
		Delivery:    grpcclient.NewDelivery(conn),
	}, func() { _ = conn.Close() }, nil
}

// NewActivities собирает Activities fulfillment поверх порта Order и
// адаптеров соседних контекстов. Workflow-код зависимостей не получает: внешние
// операции выполняются только этими Activities (ADR 5).
func NewActivities(order orders.Orders, neighbours Neighbours) *fulfillmenttemporal.Activities {
	return &fulfillmenttemporal.Activities{
		Orders:      order,
		Payments:    neighbours.Payments,
		Restaurants: neighbours.Restaurants,
		Delivery:    neighbours.Delivery,
	}
}
