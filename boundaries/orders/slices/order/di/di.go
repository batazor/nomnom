// Package di описывает зависимости слайса order для Google Wire.
// Доменная модель и handlers use cases Wire не импортируют.
package di

import (
	"github.com/google/wire"

	"nomnom/orders/slices/order/applications/getorder"
	"nomnom/orders/slices/order/applications/listcustomerorders"
	"nomnom/orders/slices/order/infrastructure/memory"
)

// ProviderSet собирает компоненты слайса order. Provider'ы handlers
// добавляются сюда вместе с реализацией соответствующих use cases.
var ProviderSet = wire.NewSet(NewStore, NewModule)

// Module — собранный слайс order, который получает composition root процесса.
// In-memory adapters keep the current executable useful until PostgreSQL and
// SpiceDB adapters replace them in this composition root.
type Module struct {
	GetOrder           getorder.Handler
	ListCustomerOrders listcustomerorders.Handler
}

// NewStore is the in-memory repository, Inbox and Outbox of the slice shared by
// the components of one process until PostgreSQL replaces it.
func NewStore() *memory.Store {
	return memory.NewStore()
}

func NewModule(store *memory.Store) *Module {
	authorizer := memory.NewAuthorizer()
	return &Module{
		GetOrder:           getorder.NewHandler(authorizer, store),
		ListCustomerOrders: listcustomerorders.NewHandler(authorizer, store),
	}
}
