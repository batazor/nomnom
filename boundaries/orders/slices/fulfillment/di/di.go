// Package di описывает зависимости слайса fulfillment для Google Wire.
// Доменная модель процесса и workflow Wire не импортируют.
package di

import (
	"nomnom/orders/pkg/application"
	gatewayclient "nomnom/orders/pkg/gateway"
	ports "nomnom/orders/slices/fulfillment/applications/neighbours"

	"github.com/google/wire"

	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/fulfillment/infrastructure/neighbours/grpcclient"
	fulfillmenttemporal "nomnom/orders/slices/fulfillment/infrastructure/temporal"
	"nomnom/orders/slices/order/applications/completeorder"
	"nomnom/orders/slices/order/applications/failorcancelorder"
	"nomnom/orders/slices/order/applications/planschedule"
	"nomnom/orders/slices/order/applications/recordpaymentauthorization"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/estimates"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// WorkerSet — Activities fulfillment orders-worker и адаптеры соседних
// контекстов. Store процесса и телеметрию use cases даёт слайс order
// (RepositorySet, TelemetrySet); роль Store связывает composition root.
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
	Payments    ports.Payments
	Restaurants ports.Restaurants
	Delivery    ports.Delivery
	// Preparation and DeliveryDuration are the estimates of Plan Schedule.
	Preparation      planschedule.Preparation
	DeliveryDuration planschedule.DeliveryDuration
}

// NewNeighbours — gRPC-клиенты payments, restaurants и delivery через internal
// gateway; cleanup закрывает соединение.
func NewNeighbours(gateway config.InternalGatewayConfig, identity config.IdentityConfig) (Neighbours, func(), error) {
	conn, err := gatewayclient.Dial(gateway.Address, identity.TokenDir)
	if err != nil {
		return Neighbours{}, nil, err
	}
	return Neighbours{
		Payments:         grpcclient.NewPayments(conn),
		Restaurants:      grpcclient.NewRestaurants(conn),
		Delivery:         grpcclient.NewDelivery(conn),
		Preparation:      estimates.NewRestaurants(conn),
		DeliveryDuration: estimates.NewDelivery(conn),
	}, func() { _ = conn.Close() }, nil
}

// NewActivities собирает Activities fulfillment поверх store слайса order и
// адаптеров соседних контекстов. Workflow-код зависимостей не получает: внешние
// операции выполняются только этими Activities (ADR 5).
//
// Plan Schedule takes SCHEDULED_ORDER__PREPARATION_BUFFER and
// SCHEDULED_ORDER__REPLAN_LEAD, validated at startup.
func NewActivities(store Store, neighbours Neighbours, uc *telemetry.UseCases, scheduled config.ScheduledOrderConfig) *fulfillmenttemporal.Activities {
	plan := planschedule.NewHandler(store, neighbours.Preparation, neighbours.DeliveryDuration, order.SchedulePolicy{
		PreparationBuffer: scheduled.PreparationBuffer,
		ReplanLead:        scheduled.ReplanLead,
	})
	return &fulfillmenttemporal.Activities{
		Orders:        store,
		Payments:      neighbours.Payments,
		Restaurants:   neighbours.Restaurants,
		Delivery:      neighbours.Delivery,
		RecordPayment: telemetry.Instrument(uc, observability.RecordPaymentAuthorization, recordpaymentauthorization.NewHandler(store, store).Handle),
		Complete:      telemetry.Instrument(uc, observability.CompleteOrder, completeorder.NewHandler(store, store, store).Handle),
		Finalize:      telemetry.Instrument(uc, observability.FailOrCancelOrder, failorcancelorder.NewHandler(store, store, store).Handle),
		Plan:          telemetry.Instrument(uc, observability.PlanSchedule, plan.Handle),
	}
}
