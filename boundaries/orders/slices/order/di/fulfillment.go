package di

import (
	gatewayclient "nomnom/orders/infrastructure/gateway"
	"nomnom/orders/pkg/config"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/closure/complete"
	"nomnom/orders/slices/order/applications/closure/failorcancel"
	"nomnom/orders/slices/order/applications/fulfillmentsteps/recordpaymentauthorization"
	"nomnom/orders/slices/order/applications/getforfulfillment"
	"nomnom/orders/slices/order/applications/planschedule"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/estimates"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// FulfillmentModule — use cases слайса order, которые вызывают Activities Order
// Fulfillment. Слайс fulfillment получает их собранными и не знает их портов.
type FulfillmentModule struct {
	GetForFulfillment usecase.Handler[getforfulfillment.Query, getforfulfillment.View]
	RecordPayment     usecase.Handler[recordpaymentauthorization.Input, recordpaymentauthorization.Result]
	Complete          usecase.Handler[complete.Input, complete.Result]
	Finalize          usecase.Handler[failorcancel.Input, failorcancel.Result]
	Plan              usecase.Handler[planschedule.Input, order.SchedulePlan]
}

// FulfillmentStore — роль хранилища FulfillmentModule: чтение Order, Unit of Work,
// repository и Outbox use cases Complete Order и Fail Or Cancel Order.
type FulfillmentStore interface {
	unitofwork.UnitOfWork
	order.Repository
	complete.Outbox
}

// FulfillmentEstimates — оценки restaurants и delivery для Plan Schedule.
type FulfillmentEstimates struct {
	Preparation      planschedule.Preparation
	DeliveryDuration planschedule.DeliveryDuration
}

// FulfillmentDependencies — порты use cases FulfillmentModule. Wire заполняет все
// поля (wire.Struct), тесты передают их литералом.
type FulfillmentDependencies struct {
	Store     FulfillmentStore
	Estimates FulfillmentEstimates
	Scheduled config.ScheduledOrderConfig
	Telemetry *telemetry.UseCases
}

// NewFulfillmentEstimates — gRPC-клиенты оценок через internal gateway; cleanup
// закрывает соединение.
func NewFulfillmentEstimates(gateway config.InternalGatewayConfig, identity config.IdentityConfig) (FulfillmentEstimates, func(), error) {
	conn, err := gatewayclient.Dial(gateway.Address, identity.TokenDir)
	if err != nil {
		return FulfillmentEstimates{}, nil, err
	}
	return FulfillmentEstimates{
		Preparation:      estimates.NewRestaurants(conn),
		DeliveryDuration: estimates.NewDelivery(conn),
	}, func() { _ = conn.Close() }, nil
}

// NewFulfillmentModule собирает handlers под декоратором телеметрии use cases.
//
// Plan Schedule takes SCHEDULED_ORDER__PREPARATION_BUFFER and
// SCHEDULED_ORDER__REPLAN_LEAD, validated at startup.
func NewFulfillmentModule(deps FulfillmentDependencies) *FulfillmentModule {
	store, uc := deps.Store, deps.Telemetry
	plan := planschedule.NewHandler(store, deps.Estimates.Preparation, deps.Estimates.DeliveryDuration, order.SchedulePolicy{
		PreparationBuffer: deps.Scheduled.PreparationBuffer,
		ReplanLead:        deps.Scheduled.ReplanLead,
	})
	return &FulfillmentModule{
		GetForFulfillment: telemetry.Instrument(uc, observability.GetFulfillmentOrder, getforfulfillment.NewHandler(store).Handle),
		RecordPayment:     telemetry.Instrument(uc, observability.RecordPaymentAuthorization, recordpaymentauthorization.NewHandler(store, store).Handle),
		Complete:          telemetry.Instrument(uc, observability.CompleteOrder, complete.NewHandler(store, store, store).Handle),
		Finalize:          telemetry.Instrument(uc, observability.FailOrCancelOrder, failorcancel.NewHandler(store, store, store).Handle),
		Plan:              telemetry.Instrument(uc, observability.PlanSchedule, plan.Handle),
	}
}
