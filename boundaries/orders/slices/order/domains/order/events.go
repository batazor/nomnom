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
	MarketID     vo.MarketID
	Total        vo.Money
	PlacedAt     time.Time
	// Items is the composition at placement; restaurants creates its Restaurant
	// Order from it.
	Items []Item
	// DeliveryWindow is set for a Scheduled Order: the workflow takes the
	// scheduled route.
	DeliveryWindow DeliveryWindow
}

func (OrderPlaced) isOrderDomainEvent() {}

type OrderCancelled struct {
	OrderID     ID
	MarketID    vo.MarketID
	RequestedAt time.Time
	Reason      CancellationReason
}

func (OrderCancelled) isOrderDomainEvent() {}

type OrderCompleted struct {
	OrderID  ID
	MarketID vo.MarketID
	// PlacedAt is the start of fulfillment, carried for its duration.
	PlacedAt time.Time
	// CompletedAt is the moment of completion and the time of the event.
	CompletedAt time.Time
}

func (OrderCompleted) isOrderDomainEvent() {}

func (o *Order) PullEvents() []DomainEvent {
	events := append([]DomainEvent(nil), o.events...)
	o.events = nil
	return events
}
