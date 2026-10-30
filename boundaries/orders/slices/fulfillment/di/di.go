// Package di описывает зависимости слайса fulfillment для Google Wire.
// Доменная модель процесса и workflow Wire не импортируют.
package di

import (
	gatewayclient "nomnom/orders/infrastructure/gateway"
	ports "nomnom/orders/slices/fulfillment/applications/neighbours"

	"github.com/google/wire"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/fulfillment/infrastructure/neighbours/grpcclient"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
	orderdi "nomnom/orders/slices/order/di"
)

// WorkerSet — Activities fulfillment orders-worker и адаптеры соседних
// контекстов. Use cases Order даёт слайс order (orderdi.FulfillmentSet).
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

// NewActivities собирает Activities fulfillment поверх use cases слайса order и
// адаптеров соседних контекстов. Workflow-код зависимостей не получает: внешние
// операции выполняются только этими Activities (ADR 5).
func NewActivities(order *orderdi.FulfillmentModule, neighbours Neighbours) *fulfillmenttemporal.Activities {
	return &fulfillmenttemporal.Activities{
		Orders:        order.GetOrder,
		Payments:      neighbours.Payments,
		Restaurants:   neighbours.Restaurants,
		Delivery:      neighbours.Delivery,
		RecordPayment: order.RecordPayment,
		Complete:      order.Complete,
		Finalize:      order.Finalize,
		Plan:          order.Plan,
	}
}
