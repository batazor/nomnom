// Package neighbours provides interim adapters of the read APIs of neighbouring
// contexts used by order use cases until those APIs exist. Client adapters of
// the fulfillment Activities live in slices/fulfillment/infrastructure/neighbours.
package neighbours

import (
	"context"
	"errors"
	"fmt"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/placeorder"
	"nomnom/orders/slices/order/domains/order"
)

// ErrNotImplemented means the API of the neighbouring context is not published yet.
var ErrNotImplemented = errors.New("command API of the neighbouring context is not implemented")

// UnavailableCatalog is the placeorder.Catalog of the API until restaurants
// publishes a menu read API: it fails closed, so no Order is placed with prices
// orders cannot verify.
type UnavailableCatalog struct{}

var _ placeorder.Catalog = UnavailableCatalog{}

func (UnavailableCatalog) Snapshot(context.Context, order.RestaurantID, []order.MenuItemID) (placeorder.CatalogSnapshot, error) {
	return placeorder.CatalogSnapshot{}, fmt.Errorf("restaurants menu: %w: %w", application.ErrDependencyUnavailable, ErrNotImplemented)
}
