package di

import (
	"context"
	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/applications/requestordercancellation"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// Module — собранный слайс order, который получает composition root процесса.
type Module struct {
	PlaceOrder               application.Handler[placeorder.Input, placeorder.Result]
	RequestOrderCancellation application.Handler[requestordercancellation.Input, requestordercancellation.Result]
	GetOrder                 application.Handler[getorder.Query, orderdetails.View]
	ListCustomerOrders       application.Handler[listcustomerorders.Query, listcustomerorders.Page]
}

// PlacementStore — роль хранилища command use cases API: Unit of Work,
// repository и Outbox Place Order и Request Order Cancellation, idempotency
// keys и authorization revision Place Order.
type PlacementStore interface {
	application.UnitOfWork
	order.Repository
	placeorder.Outbox
	placeorder.IdempotencyKeys
	placeorder.Revisions
}

// ReadModels — роль хранилища queries API и gauge состояний.
type ReadModels interface {
	getorder.Reader
	listcustomerorders.Reader
	CountByState(ctx context.Context) (map[order.State]int64, error)
}

// Dependencies — порты use cases Module. Wire заполняет все поля
// (wire.Struct) одним store процесса во всех ролях, тесты передают in-memory
// реализации литералом.
type Dependencies struct {
	Commands   PlacementStore
	ReadModels ReadModels
	Authorizer Authorizer
	Policy     CancellationPolicy
	Catalog    placeorder.Catalog
	Workflow   Workflow
	Clock      application.Clock
	IDs        placeorder.IDGenerator
	Telemetry  *telemetry.UseCases
	Metrics    *observability.DomainMetrics
}

// NewModule собирает handlers под декоратором телеметрии use cases. Get Order
// и Request Order Cancellation получают один экземпляр cancellation policy.
func NewModule(deps Dependencies) *Module {
	store, authorizer, uc := deps.Commands, deps.Authorizer, deps.Telemetry
	placeOrder := placeorder.NewHandler(placeorder.Dependencies{
		UnitOfWork:      store,
		Orders:          store,
		Outbox:          store,
		IdempotencyKeys: store,
		Catalog:         deps.Catalog,
		Relationships:   authorizer,
		Revisions:       store,
		IDs:             deps.IDs,
		Clock:           deps.Clock,
		Workflow:        deps.Workflow,
	})
	requestCancellation := requestordercancellation.NewHandler(requestordercancellation.Dependencies{
		Authorizer: authorizer,
		Orders:     store,
		Policy:     deps.Policy,
		Clock:      deps.Clock,
		Workflow:   deps.Workflow,
	})
	if deps.Metrics != nil {
		// Gauge состояний — только у API: одна роль на процесс, без дублей.
		observability.ObserveOrdersInState(deps.ReadModels.CountByState)
	}
	return &Module{
		PlaceOrder: telemetry.Instrument(uc, observability.PlaceOrder, placeOrder.Handle),
		RequestOrderCancellation: telemetry.Instrument(uc, observability.RequestOrderCancellation,
			observability.CancellationRequests(deps.Metrics, requestCancellation.Handle)),
		GetOrder:           telemetry.Instrument(uc, observability.GetOrder, getorder.NewHandler(authorizer, deps.ReadModels, deps.Policy).Handle),
		ListCustomerOrders: telemetry.Instrument(uc, observability.ListCustomerOrders, listcustomerorders.NewHandler(authorizer, deps.ReadModels, deps.Policy).Handle),
	}
}
