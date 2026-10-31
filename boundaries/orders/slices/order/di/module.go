package di

import (
	"context"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/closure/requestcancellation"
	"nomnom/orders/slices/order/applications/get"
	"nomnom/orders/slices/order/applications/listforcustomer"
	"nomnom/orders/slices/order/applications/orderdetails"
	"nomnom/orders/slices/order/applications/place"
	"nomnom/orders/slices/order/applications/syncrelationships"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// Module — собранный слайс order, который получает composition root процесса.
type Module struct {
	PlaceOrder               usecase.Handler[place.Input, place.Result]
	RequestOrderCancellation usecase.Handler[requestcancellation.Input, requestcancellation.Result]
	GetOrder                 usecase.Handler[get.Query, orderdetails.View]
	ListCustomerOrders       usecase.Handler[listforcustomer.Query, listforcustomer.Page]
}

// PlacementStore — роль хранилища command use cases API: Unit of Work,
// repository и Outbox Place Order и Request Order Cancellation, idempotency
// keys и RelationshipsChanged нового Order.
type PlacementStore interface {
	unitofwork.UnitOfWork
	order.Repository
	place.Outbox
	place.IdempotencyKeys
	syncrelationships.Changes
}

// ReadModels — роль хранилища queries API и gauge состояний.
type ReadModels interface {
	get.Reader
	listforcustomer.Reader
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
	Catalog    place.Catalog
	Slots      place.SlotReservations
	Workflow   Workflow
	Clock      usecase.Clock
	IDs        place.IDGenerator
	Telemetry  *telemetry.UseCases
	Metrics    *observability.DomainMetrics
}

// NewModule собирает handlers под декоратором телеметрии use cases. Get Order
// и Request Order Cancellation получают один экземпляр cancellation policy.
func NewModule(deps Dependencies) *Module {
	store, authorizer, uc := deps.Commands, deps.Authorizer, deps.Telemetry
	placeOrder := place.NewHandler(place.Dependencies{
		UnitOfWork:      store,
		Orders:          store,
		Outbox:          store,
		IdempotencyKeys: store,
		Catalog:         deps.Catalog,
		Slots:           deps.Slots,
		Relationships:   store,
		IDs:             deps.IDs,
		Clock:           deps.Clock,
	})
	requestCancellation := requestcancellation.NewHandler(requestcancellation.Dependencies{
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
		GetOrder:           telemetry.Instrument(uc, observability.GetOrder, get.NewHandler(authorizer, deps.ReadModels, deps.Policy).Handle),
		ListCustomerOrders: telemetry.Instrument(uc, observability.ListCustomerOrders, listforcustomer.NewHandler(authorizer, deps.ReadModels, deps.Policy).Handle),
	}
}
