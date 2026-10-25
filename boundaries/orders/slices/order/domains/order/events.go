package order

import (
	"time"

	"nomnom/orders/slices/order/domains/order/vo"
)

type DomainEvent interface {
	isOrderDomainEvent()
}

type OrderPlaced struct {
	OrderID      ID
	CustomerID   CustomerID
	RestaurantID RestaurantID
	Total        vo.Money
	PlacedAt     time.Time
}

func (OrderPlaced) isOrderDomainEvent() {}

type OrderCancelled struct {
	OrderID     ID
	RequestedAt time.Time
	Reason      CancellationReason
}

func (OrderCancelled) isOrderDomainEvent() {}

type OrderCompleted struct {
	OrderID ID
}

func (OrderCompleted) isOrderDomainEvent() {}

func (o *Order) PullEvents() []DomainEvent {
	events := append([]DomainEvent(nil), o.events...)
	o.events = nil
	return events
}
