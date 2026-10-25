// Package restaurants implements the catalog port of the customer Simulator on
// the public reads ListRestaurants and GetMenu of restaurants, called through
// the internal gateway like the Place Order catalog.
package restaurants

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	restaurantsv1 "nomnom/orders/pkg/gateway/gen/nomnom/restaurants/v1"
	"nomnom/orders/slices/order/domains/order"
	"nomnom/orders/slices/simulator/applications/customersimulator"
)

// pageSize is the maximum page of ListRestaurants.
const pageSize = 100

type Catalog struct {
	client  restaurantsv1.RestaurantsServiceClient
	timeout time.Duration
}

var _ customersimulator.Catalog = (*Catalog)(nil)

func New(conn grpc.ClientConnInterface, timeout time.Duration) *Catalog {
	return &Catalog{client: restaurantsv1.NewRestaurantsServiceClient(conn), timeout: timeout}
}

// OpenRestaurants reads every page of the open restaurants.
func (c *Catalog) OpenRestaurants(ctx context.Context) ([]order.RestaurantID, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var (
		ids   []order.RestaurantID
		token string
	)
	for {
		page, err := c.client.ListRestaurants(ctx, &restaurantsv1.ListRestaurantsRequest{PageSize: pageSize, PageToken: token})
		if err != nil {
			return nil, fmt.Errorf("restaurants.ListRestaurants: %w", err)
		}
		for _, restaurant := range page.GetRestaurants() {
			if restaurant.GetIsOpen() {
				ids = append(ids, order.RestaurantID(restaurant.GetRestaurantId()))
			}
		}
		token = page.GetNextPageToken()
		if token == "" {
			return ids, nil
		}
	}
}

// AvailableMenuItems returns the Menu Items currently available to order.
func (c *Catalog) AvailableMenuItems(ctx context.Context, restaurantID order.RestaurantID) ([]order.MenuItemID, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	menu, err := c.client.GetMenu(ctx, &restaurantsv1.GetMenuRequest{RestaurantId: string(restaurantID)})
	if err != nil {
		return nil, fmt.Errorf("restaurants.GetMenu %q: %w", restaurantID, err)
	}
	var ids []order.MenuItemID
	for _, item := range menu.GetItems() {
		if item.GetAvailable() {
			ids = append(ids, order.MenuItemID(item.GetMenuItemId()))
		}
	}
	return ids, nil
}
