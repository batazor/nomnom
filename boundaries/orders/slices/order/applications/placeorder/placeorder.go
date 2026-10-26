// Package placeorder places an Order for the calling Customer from a verified
// restaurant catalog snapshot and requests the start of its fulfillment.
package placeorder

import (
	"context"
	"errors"
	"fmt"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// ErrUnknownRestaurant is returned by Catalog adapters for a restaurant without a menu.
var ErrUnknownRestaurant = errors.New("unknown restaurant")

// ErrRestaurantNotAcceptingOrders is returned by Catalog adapters for a closed
// restaurant: the Order is refused before the transaction.
var ErrRestaurantNotAcceptingOrders = errors.New("restaurant is not accepting orders")

type ItemInput struct {
	MenuItemID order.MenuItemID
	Quantity   int
}

type Input struct {
	// Subject must be the verified Customer placing the Order.
	Subject application.Subject
	// IdempotencyKey is chosen by the client and is unique per Customer.
	IdempotencyKey string
	RestaurantID   order.RestaurantID
	Items          []ItemInput
}

type Result struct {
	OrderID order.ID
	// Replayed reports that the IdempotencyKey already identified a placed Order.
	Replayed bool
	// FulfillmentStarted is false when the post-commit start failed; repeating
	// the request with the same IdempotencyKey retries the start.
	FulfillmentStarted bool
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
	UnitOfWork      application.UnitOfWork
	Orders          order.Repository
	Outbox          Outbox
	IdempotencyKeys IdempotencyKeys
	Catalog         Catalog
	// Relationships records RelationshipsChanged of the new Order in the
	// placement transaction: orders-consumer writes order#customer and
	// order#restaurant afterwards (ADR 18). The Customer reads and cancels the
	// own Order by customer_id before that.
	Relationships syncorderrelationships.Changes
	IDs           IDGenerator
	Clock         application.Clock
	Workflow      fulfillment.Starter
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
		return h.start(ctx, Result{OrderID: existing, Replayed: true}), nil
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

	orderID, err := h.deps.IDs.NewOrderID(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("generate order id: %w", err)
	}
	placed, err := order.Place(orderID, customerID, in.RestaurantID, snapshot.MarketID, items, h.deps.Clock.Now(), orderability)
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
	return h.start(ctx, result), nil
}

// replayConcurrent reads the Order a concurrent request committed under key.
func (h Handler) replayConcurrent(ctx context.Context, customerID order.CustomerID, key string) (Result, error) {
	existing, found, err := h.deps.IdempotencyKeys.FindOrder(ctx, customerID, key)
	if err != nil {
		return Result{}, fmt.Errorf("find idempotency key: %w", err)
	}
	if !found {
		return Result{}, fmt.Errorf("%w: idempotency key %q is being placed concurrently", application.ErrConcurrencyConflict, key)
	}
	return Result{OrderID: existing, Replayed: true}, nil
}

// start runs after commit; the committed Order and OrderPlaced in the Outbox
// stay valid when the workflow is temporarily unavailable.
func (h Handler) start(ctx context.Context, result Result) Result {
	result.FulfillmentStarted = h.deps.Workflow.Start(ctx, fulfillment.StartRequest{OrderID: result.OrderID}) == nil
	return result
}

type itemRequest struct {
	menuItemID order.MenuItemID
	quantity   vo.Quantity
}

type placementRequest struct {
	items []itemRequest
}

// validate rejects malformed input before any external call. The aggregate
// re-checks its invariants when the Order is created.
func validate(in Input) (order.CustomerID, placementRequest, error) {
	if err := in.Subject.Validate(); err != nil {
		return "", placementRequest{}, err
	}
	if in.Subject.Kind != application.SubjectUser {
		return "", placementRequest{}, fmt.Errorf("%w: only a customer places an order", application.ErrPermissionDenied)
	}
	if in.IdempotencyKey == "" {
		return "", placementRequest{}, application.InvalidInput("idempotency_key", "is required")
	}
	if in.RestaurantID == "" {
		return "", placementRequest{}, application.InvalidInput("restaurant_id", "is required")
	}
	if len(in.Items) == 0 {
		return "", placementRequest{}, order.ErrOrderRequiresItems
	}

	request := placementRequest{items: make([]itemRequest, 0, len(in.Items))}
	seen := make(map[order.MenuItemID]struct{}, len(in.Items))
	for _, item := range in.Items {
		if item.MenuItemID == "" {
			return "", placementRequest{}, application.InvalidInput("menu_item_id", "is required")
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
