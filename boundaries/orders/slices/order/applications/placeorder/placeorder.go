// Package placeorder places an Order for the calling Customer from a verified
// restaurant catalog snapshot and requests the start of its fulfillment.
package placeorder

import (
	"context"
	"errors"
	"fmt"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// ErrUnknownRestaurant is returned by Catalog adapters for a restaurant without a menu.
var ErrUnknownRestaurant = errors.New("unknown restaurant")

type ItemInput struct {
	MenuItemID order.MenuItemID
	Quantity   int
}

type Input struct {
	// Subject must be the verified Customer placing the Order.
	Subject application.Subject
	// IdempotencyKey is chosen by the client and is unique per Customer.
	IdempotencyKey  string
	RestaurantID    order.RestaurantID
	Items           []ItemInput
	DeliveryAddress string
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
	UnitPrice vo.Money
	Available bool
}

// CatalogSnapshot is read from restaurants before the transaction. Menu Items
// missing from Items are treated as unavailable.
type CatalogSnapshot struct {
	Currency     string
	Items        map[order.MenuItemID]CatalogItem
	MinimumTotal vo.Money
	MaximumTotal vo.Money
}

type Catalog interface {
	Snapshot(ctx context.Context, restaurantID order.RestaurantID, menuItemIDs []order.MenuItemID) (CatalogSnapshot, error)
}

// Relationships writes the creation relationship Customer → Order before commit.
type Relationships interface {
	GrantOrderCustomer(ctx context.Context, orderID order.ID, customerID order.CustomerID) error
}

type IDGenerator interface {
	NewOrderID(ctx context.Context) (order.ID, error)
}

type IdempotencyKeys interface {
	FindOrder(ctx context.Context, customerID order.CustomerID, key string) (order.ID, bool, error)
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
	Relationships   Relationships
	IDs             IDGenerator
	Clock           application.Clock
	Workflow        fulfillment.Starter
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
	items, orderability, err := request.priced(snapshot)
	if err != nil {
		return Result{}, err
	}

	orderID, err := h.deps.IDs.NewOrderID(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("generate order id: %w", err)
	}
	placed, err := order.Place(orderID, customerID, in.RestaurantID, items, request.address, h.deps.Clock.Now(), orderability)
	if err != nil {
		return Result{}, err
	}
	if err := h.deps.Relationships.GrantOrderCustomer(ctx, orderID, customerID); err != nil {
		return Result{}, fmt.Errorf("write order customer relationship: %w", err)
	}

	// Events are pulled once so that a retried transaction writes them again.
	events := placed.PullEvents()
	result := Result{OrderID: orderID}
	err = h.deps.UnitOfWork.Do(ctx, func(ctx context.Context) error {
		// A concurrent request with the same key may have committed meanwhile;
		// the relationship written above is then left to reconciliation.
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
		return h.deps.Outbox.Append(ctx, orderID, placed.Version()+1, events)
	})
	if err != nil {
		return Result{}, err
	}
	return h.start(ctx, result), nil
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
	items   []itemRequest
	address vo.DeliveryAddress
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

	address, err := vo.NewDeliveryAddress(in.DeliveryAddress)
	if err != nil {
		return "", placementRequest{}, err
	}
	request.address = address
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
		items = append(items, order.NewItem(item.menuItemID, item.quantity, price))
	}
	return items, order.OrderabilityContext{
		Availability: availability,
		MinimumTotal: snapshot.MinimumTotal,
		MaximumTotal: snapshot.MaximumTotal,
	}, nil
}
