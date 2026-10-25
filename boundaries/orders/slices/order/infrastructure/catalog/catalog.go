// Package catalog implements the placeorder.Catalog port on the public read
// RPCs GetRestaurant and GetMenu of restaurants, called through the internal
// gateway before the Place Order transaction. Order total limits are not
// published by restaurants yet and come from configuration (placeholders).
package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"nomnom/orders/pkg/application"
	restaurantsv1 "nomnom/orders/pkg/gateway/gen/nomnom/restaurants/v1"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/order/domains/order/vo"
)

// ErrMixedCurrencies means the menu of a restaurant prices its items in more
// than one currency, so no Order total can be computed.
var ErrMixedCurrencies = fmt.Errorf("%w: menu prices items in more than one currency", vo.ErrInvalidValue)

// Limits are the order total limits in the currency of the menu.
type Limits struct {
	MinimumTotal decimal.Decimal
	MaximumTotal decimal.Decimal
}

// Catalog reads the current menu of restaurants. Each Snapshot makes two calls
// bounded by one timeout: GetRestaurant for is_open, then GetMenu.
type Catalog struct {
	client  restaurantsv1.RestaurantsServiceClient
	limits  Limits
	timeout time.Duration
}

var _ placeorder.Catalog = (*Catalog)(nil)

func New(conn grpc.ClientConnInterface, limits Limits, timeout time.Duration) *Catalog {
	return &Catalog{client: restaurantsv1.NewRestaurantsServiceClient(conn), limits: limits, timeout: timeout}
}

func (c *Catalog) Snapshot(ctx context.Context, restaurantID order.RestaurantID, menuItemIDs []order.MenuItemID) (placeorder.CatalogSnapshot, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	restaurant, err := c.client.GetRestaurant(ctx, &restaurantsv1.GetRestaurantRequest{RestaurantId: string(restaurantID)})
	if err != nil {
		return placeorder.CatalogSnapshot{}, mapError("restaurants.GetRestaurant", restaurantID, err)
	}
	if !restaurant.GetRestaurant().GetIsOpen() {
		return placeorder.CatalogSnapshot{}, fmt.Errorf("restaurant %q: %w", restaurantID, placeorder.ErrRestaurantNotAcceptingOrders)
	}

	menu, err := c.client.GetMenu(ctx, &restaurantsv1.GetMenuRequest{RestaurantId: string(restaurantID)})
	if err != nil {
		return placeorder.CatalogSnapshot{}, mapError("restaurants.GetMenu", restaurantID, err)
	}
	return c.snapshot(restaurantID, menu.GetItems(), menuItemIDs)
}

// snapshot keeps only the requested Menu Items: items missing from the menu
// stay out of the snapshot and are unavailable to Place Order.
func (c *Catalog) snapshot(restaurantID order.RestaurantID, items []*restaurantsv1.MenuItem, requested []order.MenuItemID) (placeorder.CatalogSnapshot, error) {
	currency, err := menuCurrency(items)
	if err != nil {
		return placeorder.CatalogSnapshot{}, fmt.Errorf("restaurant %q: %w", restaurantID, err)
	}
	if currency == "" {
		// An empty menu is a restaurant without a menu.
		return placeorder.CatalogSnapshot{}, fmt.Errorf("restaurant %q has an empty menu: %w", restaurantID, placeorder.ErrUnknownRestaurant)
	}
	minimum, err := vo.NewMoney(c.limits.MinimumTotal, currency)
	if err != nil {
		return placeorder.CatalogSnapshot{}, fmt.Errorf("catalog minimum total: %w", err)
	}
	maximum, err := vo.NewMoney(c.limits.MaximumTotal, currency)
	if err != nil {
		return placeorder.CatalogSnapshot{}, fmt.Errorf("catalog maximum total: %w", err)
	}

	byID := make(map[order.MenuItemID]*restaurantsv1.MenuItem, len(items))
	for _, item := range items {
		byID[order.MenuItemID(item.GetMenuItemId())] = item
	}
	snapshot := placeorder.CatalogSnapshot{
		Currency:     currency,
		Items:        make(map[order.MenuItemID]placeorder.CatalogItem, len(requested)),
		MinimumTotal: minimum,
		MaximumTotal: maximum,
	}
	for _, id := range requested {
		item, known := byID[id]
		if !known {
			continue
		}
		price, err := vo.ParseMoney(item.GetPrice().GetAmount(), item.GetPrice().GetCurrency())
		if err != nil {
			return placeorder.CatalogSnapshot{}, fmt.Errorf("price of menu item %q: %w", id, err)
		}
		snapshot.Items[id] = placeorder.CatalogItem{UnitPrice: price, Available: item.GetAvailable()}
	}
	return snapshot, nil
}

// menuCurrency is the one currency of every price in the menu; empty for an
// empty menu.
func menuCurrency(items []*restaurantsv1.MenuItem) (string, error) {
	if len(items) == 0 {
		return "", nil
	}
	currency := items[0].GetPrice().GetCurrency()
	for _, item := range items[1:] {
		if item.GetPrice().GetCurrency() != currency {
			return "", ErrMixedCurrencies
		}
	}
	if _, err := vo.ZeroMoney(currency); err != nil {
		return "", fmt.Errorf("menu currency: %w", err)
	}
	return currency, nil
}

// mapError: NOT_FOUND of the restaurant or its menu is an unknown restaurant;
// INVALID_ARGUMENT is invalid input of Place Order; every other failure,
// including transport, deadline and service identity, leaves orders without a
// verified catalog and is a retryable unavailable dependency. Cancellation by
// the caller is returned as is.
func mapError(call string, restaurantID order.RestaurantID, err error) error {
	switch code := status.Code(err); {
	case code == codes.NotFound:
		return fmt.Errorf("%s %q: %w: %w", call, restaurantID, placeorder.ErrUnknownRestaurant, err)
	case code == codes.InvalidArgument:
		return fmt.Errorf("%s %q: %w: %w", call, restaurantID, application.ErrInvalidInput, err)
	case code == codes.Canceled || errors.Is(err, context.Canceled):
		return fmt.Errorf("%s %q: %w", call, restaurantID, err)
	default:
		return fmt.Errorf("%s %q: %w: %w", call, restaurantID, application.ErrDependencyUnavailable, err)
	}
}
