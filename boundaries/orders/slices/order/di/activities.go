package di

import (
	"log"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/infrastructure/neighbours"
	"nomnom/orders/slices/order/infrastructure/neighbours/grpcclient"
	"nomnom/orders/slices/order/infrastructure/observability"
	"nomnom/orders/slices/order/infrastructure/temporal/fulfillorder"
)

// Neighbours — адаптеры payments, restaurants и delivery для Activities
// fulfillment.
type Neighbours struct {
	Payments    fulfillorder.Payments
	Restaurants fulfillorder.Restaurants
	Delivery    fulfillorder.Delivery
}

// NewNeighbours выбирает адаптеры соседних контекстов по fulfillment.neighbours;
// cleanup закрывает соединение с internal gateway.
func NewNeighbours(cfg config.FulfillmentConfig, gateway config.InternalGatewayConfig, identity config.IdentityConfig) (Neighbours, func(), error) {
	switch cfg.Neighbours {
	case config.NeighboursGRPC:
		conn, err := grpcclient.Dial(gateway.Address, identity.TokenDir)
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

// NewActivities собирает Activities fulfillment поверх store слайса и адаптеров
// соседних контекстов. Workflow-код зависимостей не получает: внешние операции
// выполняются только этими Activities (ADR 5).
func NewActivities(store Store, neighbours Neighbours, uc *telemetry.UseCases) *fulfillorder.Activities {
	return &fulfillorder.Activities{
		Orders:      store,
		Payments:    neighbours.Payments,
		Restaurants: neighbours.Restaurants,
		Delivery:    neighbours.Delivery,
		Complete:    telemetry.Instrument(uc, observability.CompleteOrder, completeorder.NewHandler(store, store, store).Handle),
		Finalize:    telemetry.Instrument(uc, observability.FailOrCancelOrder, failorcancelorder.NewHandler(store, store, store).Handle),
	}
}
