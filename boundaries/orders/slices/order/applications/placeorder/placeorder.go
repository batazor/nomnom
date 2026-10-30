// Package placeorder places an Order for the calling Customer from a verified
// restaurant catalog snapshot. Its fulfillment is started by the consumer of
// the own OrderPlaced (startfulfillment), not here.
package placeorder

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nomnom/orders/pkg/authz"
	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/pkg/usecase"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// ErrUnknownRestaurant is returned by Catalog adapters for a restaurant without a menu.
var ErrUnknownRestaurant = errors.New("unknown restaurant")

// ErrRestaurantNotAcceptingOrders is returned by Catalog adapters for a closed
// restaurant: the Order is refused before the transaction.
var ErrRestaurantNotAcceptingOrders = errors.New("restaurant is not accepting orders")

// ErrPremiumUnavailable refuses Premium Priority at a restaurant without a
// premium fee (FR-650): nothing is stored.
var ErrPremiumUnavailable = errors.New("restaurant offers no premium priority")

// ErrScheduledOrdersUnavailable refuses a Scheduled Order at a restaurant
// without scheduled order settings or not accepting orders now (FR-692):
// nothing is stored.
var ErrScheduledOrdersUnavailable = errors.New("restaurant takes no scheduled orders now")

// SlotUnavailable refuses a Scheduled Order in a full Delivery Window (FR-692,
// AC-913): nothing is stored; Alternatives are the nearest available windows.
type SlotUnavailable struct {
	Alternatives []order.DeliveryWindow
}

func (e SlotUnavailable) Error() string {
	return fmt.Sprintf("delivery window is full, %d alternatives", len(e.Alternatives))
}

// SlotReservations reserves the Delivery Window of a Scheduled Order in
// restaurants with the pre-generated order id before the transaction. A
// repeat with the same order id returns the same reservation. Refusals are
// SlotUnavailable, ErrScheduledOrdersUnavailable and invalid input for a
// window off the grid, the horizon or the schedule. A hold of an Order that
// is not committed expires in restaurants (SCHEDULED_SLOT__HOLD_TTL).
type SlotReservations interface {
	Reserve(ctx context.Context, restaurantID order.RestaurantID, windowStart time.Time, orderID order.ID) (order.DeliveryWindow, error)
}

type ItemInput struct {
	MenuItemID order.MenuItemID
	Quantity   int
}

type Input struct {
	// Subject must be the verified Customer placing the Order.
	Subject authz.Subject
	// IdempotencyKey is chosen by the client and is unique per Customer.
	IdempotencyKey string
	RestaurantID   order.RestaurantID
	Items          []ItemInput
	// Priority is STANDARD or PREMIUM (FR-650); empty means STANDARD, any
	// other value is invalid input.
	Priority string
	// DeliveryWindowStart is the start of the chosen Delivery Window; set makes
	// a Scheduled Order (FR-681), zero an ASAP Order.
	DeliveryWindowStart time.Time
}

type Result struct {
	OrderID order.ID
	// Replayed reports that the IdempotencyKey already identified a placed Order.
	Replayed bool
}

// CatalogItem is the price and availability of one Menu Item at placement time.
type CatalogItem struct {
	// Name is the current Menu Item name, kept in the Order as its snapshot.
	Name      string
	UnitPrice vo.Money
	Available bool
}

// CatalogSnapshot is read from restaurants before the transaction. Menu Items
// missing from Items are treated as unavailable. MarketID is the Market of the
// Restaurant (ADR 41); the Order is placed in it.
type CatalogSnapshot struct {
	MarketID     vo.MarketID
	Currency     string
	Items        map[order.MenuItemID]CatalogItem
	MinimumTotal vo.Money
	MaximumTotal vo.Money
	// PremiumFee is the current fee of Premium Priority of the Restaurant; nil
	// when it offers no premium.
	PremiumFee *vo.Money
}

type Catalog interface {
	Snapshot(ctx context.Context, restaurantID order.RestaurantID, menuItemIDs []order.MenuItemID) (CatalogSnapshot, error)
}

type IDGenerator interface {
	NewOrderID(ctx context.Context) (order.ID, error)
}

// ErrIdempotencyKeyTaken is returned by IdempotencyKeys.RememberOrder when the
// key already identifies another Order: a concurrent request with the same key
// committed first.
var ErrIdempotencyKeyTaken = errors.New("idempotency key already identifies another order")

type IdempotencyKeys interface {
	FindOrder(ctx context.Context, customerID order.CustomerID, key string) (order.ID, bool, error)
	// RememberOrder binds key to orderID in the active transaction; it waits
	// for a concurrent transaction that binds the same key and returns
	// ErrIdempotencyKeyTaken if that one committed another Order.
	RememberOrder(ctx context.Context, customerID order.CustomerID, key string, orderID order.ID) error
}

// Outbox stores published domain events in the active transaction.
type Outbox interface {
	Append(ctx context.Context, aggregateID order.ID, aggregateVersion uint64, events []order.DomainEvent) error
}

type Dependencies struct {
	UnitOfWork      unitofwork.UnitOfWork
	Orders          order.Repository
	Outbox          Outbox
	IdempotencyKeys IdempotencyKeys
	Catalog         Catalog
	// Slots reserves the Delivery Window of a Scheduled Order (FR-681).
	Slots SlotReservations
	// Relationships records RelationshipsChanged of the new Order in the
	// placement transaction: orders-consumer writes order#customer and
	// order#restaurant afterwards (ADR 18). The Customer reads and cancels the
	// own Order by customer_id before that.
	Relationships syncorderrelationships.Changes
	IDs           IDGenerator
	Clock         usecase.Clock
}

type Handler struct {
	deps Dependencies
}

func NewHandler(deps Dependencies) Handler {
	return Handler{deps: deps}
}

func (h Handler) Handle(ctx context.Context, in Input) (Result, error) {
	customerID, request, err := validate(in)
	if err != nil {
		return Result{}, err
	}

	existing, found, err := h.deps.IdempotencyKeys.FindOrder(ctx, customerID, in.IdempotencyKey)
	if err != nil {
		return Result{}, fmt.Errorf("find idempotency key: %w", err)
	}
	if found {
		return Result{OrderID: existing, Replayed: true}, nil
	}

	snapshot, err := h.deps.Catalog.Snapshot(ctx, in.RestaurantID, request.menuItemIDs())
	if err != nil {
		return Result{}, fmt.Errorf("read catalog snapshot: %w", err)
	}
	if snapshot.MarketID.IsZero() {
		// The Catalog adapter verifies market_id of the Restaurant; a snapshot
		// without it is a defect, not invalid input of the Customer.
		return Result{}, fmt.Errorf("catalog snapshot of restaurant %q has no market_id", in.RestaurantID)
	}
	items, orderability, err := request.priced(snapshot)
	if err != nil {
		return Result{}, err
	}
	service, err := request.service(snapshot)
	if err != nil {
		return Result{}, err
	}

	orderID, err := h.deps.IDs.NewOrderID(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("generate order id: %w", err)
	}
	window, err := h.reserve(ctx, in, orderID)
	if err != nil {
		return Result{}, err
	}
	placed, err := order.Place(orderID, customerID, in.RestaurantID, snapshot.MarketID, items, service, window, h.deps.Clock.Now(), orderability)
	if err != nil {
		return Result{}, err
	}
	// Events are pulled once so that a retried transaction writes them again.
	events := placed.PullEvents()
	result := Result{OrderID: orderID}
	err = h.deps.UnitOfWork.Do(ctx, func(ctx context.Context) error {
		// A concurrent request with the same key may have committed meanwhile.
		existing, found, err := h.deps.IdempotencyKeys.FindOrder(ctx, customerID, in.IdempotencyKey)
		if err != nil {
			return fmt.Errorf("find idempotency key: %w", err)
		}
		if found {
			result = Result{OrderID: existing, Replayed: true}
			return nil
		}
		if err := h.deps.Orders.Add(ctx, placed); err != nil {
			return err
		}
		if err := h.deps.IdempotencyKeys.RememberOrder(ctx, customerID, in.IdempotencyKey, orderID); err != nil {
			return fmt.Errorf("remember idempotency key: %w", err)
		}
		if err := h.deps.Relationships.RelationshipsChanged(ctx, orderID); err != nil {
			return fmt.Errorf("record relationships change: %w", err)
		}
		return h.deps.Outbox.Append(ctx, orderID, placed.Version()+1, events)
	})
	if errors.Is(err, ErrIdempotencyKeyTaken) {
		// The concurrent request committed while this one waited on the key:
		// replay its Order.
		result, err = h.replayConcurrent(ctx, customerID, in.IdempotencyKey)
	}
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// reserve holds the Delivery Window of a Scheduled Order in restaurants under
// the pre-generated order id; the Order stores the window the reservation
// returned. An ASAP Order reserves nothing.
func (h Handler) reserve(ctx context.Context, in Input, orderID order.ID) (order.DeliveryWindow, error) {
	if in.DeliveryWindowStart.IsZero() {
		return order.DeliveryWindow{}, nil
	}
	window, err := h.deps.Slots.Reserve(ctx, in.RestaurantID, in.DeliveryWindowStart, orderID)
	if err != nil {
		return order.DeliveryWindow{}, fmt.Errorf("reserve delivery window: %w", err)
	}
	return window, nil
}

// replayConcurrent reads the Order a concurrent request committed under key.
func (h Handler) replayConcurrent(ctx context.Context, customerID order.CustomerID, key string) (Result, error) {
	existing, found, err := h.deps.IdempotencyKeys.FindOrder(ctx, customerID, key)
	if err != nil {
		return Result{}, fmt.Errorf("find idempotency key: %w", err)
	}
	if !found {
		return Result{}, fmt.Errorf("%w: idempotency key %q is being placed concurrently", usecase.ErrConcurrencyConflict, key)
	}
	return Result{OrderID: existing, Replayed: true}, nil
}

type itemRequest struct {
	menuItemID order.MenuItemID
	quantity   vo.Quantity
}

type placementRequest struct {
	items    []itemRequest
	priority order.Priority
}

// validate rejects malformed input before any external call. The aggregate
// re-checks its invariants when the Order is created.
func validate(in Input) (order.CustomerID, placementRequest, error) {
	if err := in.Subject.Validate(); err != nil {
		return "", placementRequest{}, err
	}
	if in.Subject.Kind != authz.SubjectUser {
		return "", placementRequest{}, fmt.Errorf("%w: only a customer places an order", authz.ErrPermissionDenied)
	}
	if in.IdempotencyKey == "" {
		return "", placementRequest{}, usecase.InvalidInput("idempotency_key", "is required")
	}
	if in.RestaurantID == "" {
		return "", placementRequest{}, usecase.InvalidInput("restaurant_id", "is required")
	}
	priority := order.PriorityStandard
	if in.Priority != "" {
		parsed, err := order.ParsePriority(in.Priority)
		if err != nil {
			return "", placementRequest{}, usecase.InvalidInput("priority", "must be STANDARD or PREMIUM")
		}
		priority = parsed
	}
	if len(in.Items) == 0 {
		return "", placementRequest{}, order.ErrOrderRequiresItems
	}

	request := placementRequest{items: make([]itemRequest, 0, len(in.Items)), priority: priority}
	seen := make(map[order.MenuItemID]struct{}, len(in.Items))
	for _, item := range in.Items {
		if item.MenuItemID == "" {
			return "", placementRequest{}, usecase.InvalidInput("menu_item_id", "is required")
		}
		if _, duplicate := seen[item.MenuItemID]; duplicate {
			return "", placementRequest{}, fmt.Errorf("%w: %q", order.ErrDuplicateMenuItem, item.MenuItemID)
		}
		seen[item.MenuItemID] = struct{}{}
		quantity, err := vo.NewQuantity(item.Quantity)
		if err != nil {
			return "", placementRequest{}, err
		}
		request.items = append(request.items, itemRequest{menuItemID: item.MenuItemID, quantity: quantity})
	}

	return order.CustomerID(in.Subject.ID), request, nil
}

func (r placementRequest) menuItemIDs() []order.MenuItemID {
	ids := make([]order.MenuItemID, 0, len(r.items))
	for _, item := range r.items {
		ids = append(ids, item.menuItemID)
	}
	return ids
}

// service fixes the premium fee of the snapshot in the Order: a premium
// Order at a restaurant without a fee is refused.
func (r placementRequest) service(snapshot CatalogSnapshot) (order.Service, error) {
	if r.priority == order.PriorityStandard {
		return order.StandardService(), nil
	}
	if snapshot.PremiumFee == nil {
		return order.Service{}, ErrPremiumUnavailable
	}
	return order.PremiumService(*snapshot.PremiumFee)
}

// priced maps the snapshot to domain items and the orderability context; the
// availability decision itself stays in the OrderItemsOrderable policy.
func (r placementRequest) priced(snapshot CatalogSnapshot) ([]order.Item, order.OrderabilityContext, error) {
	unknownPrice, err := vo.ZeroMoney(snapshot.Currency)
	if err != nil {
		return nil, order.OrderabilityContext{}, fmt.Errorf("catalog snapshot currency: %w", err)
	}
	items := make([]order.Item, 0, len(r.items))
	availability := make(order.ItemAvailability, len(r.items))
	for _, item := range r.items {
		entry, known := snapshot.Items[item.menuItemID]
		price := entry.UnitPrice
		if !known {
			price = unknownPrice
		}
		availability[item.menuItemID] = known && entry.Available
		items = append(items, order.NewItem(item.menuItemID, entry.Name, item.quantity, price))
	}
	return items, order.OrderabilityContext{
		Availability: availability,
		MinimumTotal: snapshot.MinimumTotal,
		MaximumTotal: snapshot.MaximumTotal,
	}, nil
}
