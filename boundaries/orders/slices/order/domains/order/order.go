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
)

type ID string
type CustomerID string
type RestaurantID string
type MenuItemID string

type Item struct {
	menuItemID MenuItemID
	quantity   vo.Quantity
	unitPrice  vo.Money
}

func NewItem(menuItemID MenuItemID, quantity vo.Quantity, unitPrice vo.Money) Item {
	return Item{menuItemID: menuItemID, quantity: quantity, unitPrice: unitPrice}
}

func (i Item) MenuItemID() MenuItemID { return i.menuItemID }
func (i Item) Quantity() vo.Quantity  { return i.quantity }
func (i Item) UnitPrice() vo.Money    { return i.unitPrice }

// NegativeOutcome distinguishes an explicit Cancellation from a business Failure.
// Both currently end in StateCancelled; a separate failed state is not agreed yet.
type NegativeOutcome string

const (
	OutcomeCancellation NegativeOutcome = "cancellation"
	OutcomeFailure      NegativeOutcome = "failure"
)

// CancellationReason keeps the kind of negative outcome and a stable reason code.
type CancellationReason struct {
	outcome NegativeOutcome
	code    string
}

func NewCancellationReason(outcome NegativeOutcome, code string) (CancellationReason, error) {
	if (outcome != OutcomeCancellation && outcome != OutcomeFailure) || code == "" {
		return CancellationReason{}, ErrInvalidCancellationReason
	}
	return CancellationReason{outcome: outcome, code: code}, nil
}

func (r CancellationReason) Outcome() NegativeOutcome { return r.outcome }
func (r CancellationReason) Code() string             { return r.code }
func (r CancellationReason) IsZero() bool             { return r == CancellationReason{} }

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

// NotOrderable wraps the reasons of an unsatisfied OrderItemsOrderable policy.
type NotOrderable struct {
	Reasons []PolicyReason
}

func (e NotOrderable) Error() string {
	return fmt.Sprintf("order items are not orderable: %v", e.Reasons)
}

// CancellationNotAllowed wraps the reasons of an unsatisfied WithinCancellationWindow policy.
type CancellationNotAllowed struct {
	Reasons []PolicyReason
}

func (e CancellationNotAllowed) Error() string {
	return fmt.Sprintf("order cancellation is not allowed: %v", e.Reasons)
}

// CancellationWindow is the cancellation policy interval supplied by the application.
type CancellationWindow struct {
	OpensAt  time.Time
	ClosesAt time.Time
}

type Order struct {
	id              ID
	customerID      CustomerID
	restaurantID    RestaurantID
	items           []Item
	deliveryAddress vo.DeliveryAddress
	total           vo.Money
	placedAt        time.Time
	state           State
	version         uint64
	facts           []RecordedFact
	cancellation    CancellationReason
	cancelledAt     time.Time
	events          []DomainEvent
}

// New builds an Order without recording a domain event; use Place for a new placement.
func New(
	id ID,
	customerID CustomerID,
	restaurantID RestaurantID,
	items []Item,
	deliveryAddress vo.DeliveryAddress,
) (*Order, error) {
	total, err := calculateTotal(items)
	if err != nil {
		return nil, err
	}
	return &Order{
		id:              id,
		customerID:      customerID,
		restaurantID:    restaurantID,
		items:           append([]Item(nil), items...),
		deliveryAddress: deliveryAddress,
		total:           total,
		state:           StatePlaced,
	}, nil
}

// Place creates a placed Order from a verified catalog snapshot and records OrderPlaced.
func Place(
	id ID,
	customerID CustomerID,
	restaurantID RestaurantID,
	items []Item,
	deliveryAddress vo.DeliveryAddress,
	placedAt time.Time,
	orderability OrderabilityContext,
) (*Order, error) {
	if placedAt.IsZero() {
		return nil, ErrPlacedAtRequired
	}
	order, err := New(id, customerID, restaurantID, items, deliveryAddress)
	if err != nil {
		return nil, err
	}
	if result := OrderItemsOrderable(order, orderability); !result.Satisfied() {
		return nil, NotOrderable(result)
	}
	order.placedAt = placedAt
	order.events = append(order.events, OrderPlaced{
		OrderID:      id,
		CustomerID:   customerID,
		RestaurantID: restaurantID,
		Total:        order.total,
		PlacedAt:     placedAt,
	})
	return order, nil
}

// Snapshot is the complete persisted form of an Order used by repositories.
type Snapshot struct {
	ID                 ID
	CustomerID         CustomerID
	RestaurantID       RestaurantID
	Items              []Item
	DeliveryAddress    vo.DeliveryAddress
	PlacedAt           time.Time
	State              State
	Version            uint64
	Facts              []RecordedFact
	CancellationReason CancellationReason
	CancelledAt        time.Time
}

func (o *Order) Snapshot() Snapshot {
	return Snapshot{
		ID:                 o.id,
		CustomerID:         o.customerID,
		RestaurantID:       o.restaurantID,
		Items:              o.Items(),
		DeliveryAddress:    o.deliveryAddress,
		PlacedAt:           o.placedAt,
		State:              o.state,
		Version:            o.version,
		Facts:              o.Facts(),
		CancellationReason: o.cancellation,
		CancelledAt:        o.cancelledAt,
	}
}

// Restore rebuilds a persisted Order without recording domain events.
func Restore(snapshot Snapshot) (*Order, error) {
	if !isKnownState(snapshot.State) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownOrderState, snapshot.State)
	}
	order, err := New(snapshot.ID, snapshot.CustomerID, snapshot.RestaurantID, snapshot.Items, snapshot.DeliveryAddress)
	if err != nil {
		return nil, err
	}
	seen := make(map[FulfillmentFact]struct{}, len(snapshot.Facts))
	for _, fact := range snapshot.Facts {
		if _, duplicate := seen[fact.Fact]; duplicate || !isKnownFact(fact.Fact) {
			return nil, fmt.Errorf("%w: fact %q", ErrInvalidSnapshot, fact.Fact)
		}
		seen[fact.Fact] = struct{}{}
	}
	order.placedAt = snapshot.PlacedAt
	order.state = snapshot.State
	order.version = snapshot.Version
	order.facts = append([]RecordedFact(nil), snapshot.Facts...)
	order.cancellation = snapshot.CancellationReason
	order.cancelledAt = snapshot.CancelledAt
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

func isKnownState(state State) bool {
	for _, candidate := range allStates {
		if candidate == state {
			return true
		}
	}
	return false
}

func (o *Order) Confirm() (TransitionResult, error) {
	return o.apply(TriggerConfirm, TransitionInput{})
}

// CheckCancellation reports whether a Cancellation Request is admissible at requestedAt
// without changing the Order: the window policy first, then the FSM row for cancel.
func (o *Order) CheckCancellation(requestedAt time.Time, window CancellationWindow) error {
	policy := WithinCancellationWindow(CancellationWindowContext{
		Now:      requestedAt,
		OpensAt:  window.OpensAt,
		ClosesAt: window.ClosesAt,
	})
	if !policy.Satisfied() {
		return CancellationNotAllowed(policy)
	}
	_, err := transition(o.state, TriggerCancel, TransitionInput{RequestedAt: requestedAt})
	return err
}

// Cancel records the final negative outcome chosen by the fulfillment process.
func (o *Order) Cancel(requestedAt time.Time, reason CancellationReason) (TransitionResult, error) {
	if reason.IsZero() {
		return TransitionResult{}, ErrInvalidCancellationReason
	}
	result, err := o.apply(TriggerCancel, TransitionInput{RequestedAt: requestedAt})
	if err != nil {
		return TransitionResult{}, err
	}
	o.cancellation = reason
	o.cancelledAt = requestedAt
	o.events = append(o.events, OrderCancelled{OrderID: o.id, RequestedAt: requestedAt, Reason: reason})
	return result, nil
}

func (o *Order) Complete() (TransitionResult, error) {
	result, err := o.apply(TriggerComplete, TransitionInput{})
	if err != nil {
		return TransitionResult{}, err
	}
	o.events = append(o.events, OrderCompleted{OrderID: o.id})
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
func (o *Order) CustomerID() CustomerID                 { return o.customerID }
func (o *Order) RestaurantID() RestaurantID             { return o.restaurantID }
func (o *Order) DeliveryAddress() vo.DeliveryAddress    { return o.deliveryAddress }
func (o *Order) PlacedAt() time.Time                    { return o.placedAt }
func (o *Order) CancellationReason() CancellationReason { return o.cancellation }
func (o *Order) CancelledAt() time.Time                 { return o.cancelledAt }

func (o *Order) Items() []Item { return append([]Item(nil), o.items...) }

func (o *Order) PullEvents() []DomainEvent {
	events := append([]DomainEvent(nil), o.events...)
	o.events = nil
	return events
}
