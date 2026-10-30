package order

import (
	"errors"
	"fmt"
	"time"

	"nomnom/orders/slices/order/domains/order/vo"
)

var (
	ErrOrderRequiresItems        = errors.New("order requires at least one item")
	ErrDuplicateMenuItem         = errors.New("menu item must not be repeated in an order")
	ErrUnknownOrderState         = errors.New("unknown order state")
	ErrPlacedAtRequired          = errors.New("order placement requires placed_at")
	ErrInvalidCancellationReason = errors.New("cancellation reason requires a known outcome and a code")
	ErrInvalidSnapshot           = errors.New("invalid order snapshot")
	ErrMarketRequired            = fmt.Errorf("%w: order requires a market_id", vo.ErrInvalidMarketID)
)

type ID string
type CustomerID string
type RestaurantID string
type MenuItemID string

// Item is one line of the Order. Name and UnitPrice are the snapshot of the
// Menu Item at Place Order: later menu changes do not change placed Orders.
// Name is empty for Orders placed before the name was kept.
type Item struct {
	menuItemID MenuItemID
	name       string
	quantity   vo.Quantity
	unitPrice  vo.Money
}

func NewItem(menuItemID MenuItemID, name string, quantity vo.Quantity, unitPrice vo.Money) Item {
	return Item{menuItemID: menuItemID, name: name, quantity: quantity, unitPrice: unitPrice}
}

func (i Item) MenuItemID() MenuItemID { return i.menuItemID }
func (i Item) Name() string           { return i.name }
func (i Item) Quantity() vo.Quantity  { return i.quantity }
func (i Item) UnitPrice() vo.Money    { return i.unitPrice }

// NotOrderable wraps the reasons of an unsatisfied OrderItemsOrderable policy.
type NotOrderable struct {
	Reasons []PolicyReason
}

func (e NotOrderable) Error() string {
	return fmt.Sprintf("order items are not orderable: %v", e.Reasons)
}

type Order struct {
	id           ID
	customerID   CustomerID
	restaurantID RestaurantID
	marketID     vo.MarketID
	items        []Item
	service      Service
	// deliveryWindow is set for a Scheduled Order (FR-681), fixed at Place Order.
	deliveryWindow DeliveryWindow
	total          vo.Money
	placedAt       time.Time
	state          State
	version        uint64
	facts          []RecordedFact
	cancellation   CancellationReason
	cancelledAt    time.Time
	expected       ExpectedTimes
	events         []DomainEvent
}

// New builds an Order without recording a domain event; use Place for a new
// placement. marketID is the Market of the Restaurant (ADR 41): required and
// immutable for the life of the Order. service is its priority with the
// premium fee fixed at placement; the fee is part of the total (FR-650).
func New(
	id ID,
	customerID CustomerID,
	restaurantID RestaurantID,
	marketID vo.MarketID,
	items []Item,
	service Service,
) (*Order, error) {
	if marketID.IsZero() {
		return nil, ErrMarketRequired
	}
	if err := service.validate(); err != nil {
		return nil, err
	}
	total, err := calculateTotal(items)
	if err != nil {
		return nil, err
	}
	if fee, premium := service.PremiumFee(); premium {
		if total, err = total.Add(fee); err != nil {
			return nil, fmt.Errorf("premium fee: %w", err)
		}
	}
	return &Order{
		id:           id,
		customerID:   customerID,
		restaurantID: restaurantID,
		marketID:     marketID,
		items:        append([]Item(nil), items...),
		service:      service,
		total:        total,
		state:        StatePlaced,
	}, nil
}

// Place creates a placed Order from a verified catalog snapshot and records
// OrderPlaced; marketID is the Market of the Restaurant in that snapshot.
// window is the Delivery Window reserved in restaurants for a Scheduled Order
// and zero for an ASAP Order.
func Place(
	id ID,
	customerID CustomerID,
	restaurantID RestaurantID,
	marketID vo.MarketID,
	items []Item,
	service Service,
	window DeliveryWindow,
	placedAt time.Time,
	orderability OrderabilityContext,
) (*Order, error) {
	if placedAt.IsZero() {
		return nil, ErrPlacedAtRequired
	}
	order, err := New(id, customerID, restaurantID, marketID, items, service)
	if err != nil {
		return nil, err
	}
	if result := OrderItemsOrderable(order, orderability); !result.Satisfied() {
		return nil, NotOrderable(result)
	}
	order.placedAt = placedAt
	order.deliveryWindow = window
	order.events = append(order.events, OrderPlaced{
		OrderID:        id,
		CustomerID:     customerID,
		RestaurantID:   restaurantID,
		MarketID:       marketID,
		Total:          order.total,
		PlacedAt:       placedAt,
		Items:          order.Items(),
		DeliveryWindow: window,
	})
	return order, nil
}

func calculateTotal(items []Item) (vo.Money, error) {
	if len(items) == 0 {
		return vo.Money{}, ErrOrderRequiresItems
	}

	seen := make(map[MenuItemID]struct{}, len(items))
	var total vo.Money
	for index, item := range items {
		if _, exists := seen[item.menuItemID]; exists {
			return vo.Money{}, fmt.Errorf("%w: %q", ErrDuplicateMenuItem, item.menuItemID)
		}
		seen[item.menuItemID] = struct{}{}

		lineTotal := item.unitPrice.Multiply(item.quantity)
		if index == 0 {
			total = lineTotal
			continue
		}

		var err error
		total, err = total.Add(lineTotal)
		if err != nil {
			return vo.Money{}, err
		}
	}
	return total, nil
}

// Confirm reflects restaurant Order Acceptance and records when it happened
// as the restaurant_accepted fact, in the same change of the Order.
func (o *Order) Confirm(occurredAt time.Time) (TransitionResult, error) {
	return o.recordFact(TriggerConfirm, FactRestaurantAccepted, occurredAt, "")
}

// Complete records the successful outcome chosen by the fulfillment process
// at completedAt; OrderCompleted carries that moment as its event time.
func (o *Order) Complete(completedAt time.Time) (TransitionResult, error) {
	result, err := o.apply(TriggerComplete, TransitionInput{OccurredAt: completedAt})
	if err != nil {
		return TransitionResult{}, err
	}
	o.events = append(o.events, OrderCompleted{OrderID: o.id, MarketID: o.marketID, PlacedAt: o.placedAt, CompletedAt: completedAt})
	return result, nil
}

func (o *Order) apply(trigger Trigger, input TransitionInput) (TransitionResult, error) {
	result, err := transition(o.state, trigger, input)
	if err != nil {
		return TransitionResult{}, err
	}
	o.state = result.To
	return result, nil
}

func (o *Order) ID() ID                                 { return o.id }
func (o *Order) State() State                           { return o.state }
func (o *Order) Version() uint64                        { return o.version }
func (o *Order) Total() vo.Money                        { return o.total }
func (o *Order) Service() Service                       { return o.service }
func (o *Order) CustomerID() CustomerID                 { return o.customerID }
func (o *Order) RestaurantID() RestaurantID             { return o.restaurantID }
func (o *Order) MarketID() vo.MarketID                  { return o.marketID }
func (o *Order) PlacedAt() time.Time                    { return o.placedAt }
func (o *Order) CancellationReason() CancellationReason { return o.cancellation }
func (o *Order) CancelledAt() time.Time                 { return o.cancelledAt }

func (o *Order) Items() []Item { return append([]Item(nil), o.items...) }
