package neighbours

import (
	"context"
	"fmt"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
)

// UnavailableCatalog is the placeorder.Catalog of the API until restaurants
// publishes a menu read API: it fails closed, so no Order is placed with prices
// orders cannot verify.
type UnavailableCatalog struct{}

var _ placeorder.Catalog = UnavailableCatalog{}

func (UnavailableCatalog) Snapshot(context.Context, order.RestaurantID, []order.MenuItemID) (placeorder.CatalogSnapshot, error) {
	return placeorder.CatalogSnapshot{}, fmt.Errorf("restaurants menu: %w: %w", application.ErrDependencyUnavailable, ErrNotImplemented)
}
