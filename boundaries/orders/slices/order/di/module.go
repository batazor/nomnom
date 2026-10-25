package di

import (
	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/applications/requestordercancellation"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// Module — собранный слайс order, который получает composition root процесса.
type Module struct {
	PlaceOrder               application.Handler[placeorder.Input, placeorder.Result]
	RequestOrderCancellation application.Handler[requestordercancellation.Input, requestordercancellation.Result]
	GetOrder                 application.Handler[getorder.Query, getorder.OrderView]
	ListCustomerOrders       application.Handler[listcustomerorders.Query, listcustomerorders.Page]
}

// Dependencies — порты use cases Module. Wire заполняет все поля
// (wire.Struct), тесты передают in-memory реализации литералом.
type Dependencies struct {
	Store      Store
	Authorizer Authorizer
	Policy     CancellationPolicy
	Catalog    placeorder.Catalog
	Workflow   Workflow
	Clock      application.Clock
	IDs        placeorder.IDGenerator
	Telemetry  *telemetry.UseCases
}

// NewModule собирает handlers под декоратором телеметрии use cases. Get Order
// и Request Order Cancellation получают один экземпляр cancellation policy.
func NewModule(deps Dependencies) *Module {
	store, authorizer, uc := deps.Store, deps.Authorizer, deps.Telemetry
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
	return &Module{
		PlaceOrder:               telemetry.Instrument(uc, observability.PlaceOrder, placeOrder.Handle),
		RequestOrderCancellation: telemetry.Instrument(uc, observability.RequestOrderCancellation, requestCancellation.Handle),
		GetOrder:                 telemetry.Instrument(uc, observability.GetOrder, getorder.NewHandler(authorizer, store, deps.Policy).Handle),
		ListCustomerOrders:       telemetry.Instrument(uc, observability.ListCustomerOrders, listcustomerorders.NewHandler(authorizer, store).Handle),
	}
}
