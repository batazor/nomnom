package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// Catalog implements applications.Catalog on the public reads of
// restaurants.v1.RestaurantsService. They carry no user assertion.
type Catalog struct {
	client restaurantsv1.RestaurantsServiceClient
}

func NewCatalog(conn grpc.ClientConnInterface) *Catalog {
	return &Catalog{client: restaurantsv1.NewRestaurantsServiceClient(conn)}
}

func (c *Catalog) Restaurant(ctx context.Context, restaurantID string) (applications.Restaurant, error) {
	response, err := c.client.GetRestaurant(identity.WithoutUser(ctx), &restaurantsv1.GetRestaurantRequest{RestaurantId: restaurantID})
	if err != nil {
		return applications.Restaurant{}, fmt.Errorf("get restaurant: %w", err)
	}
	restaurant := response.GetRestaurant()
	return applications.Restaurant{
		ID:      restaurant.GetRestaurantId(),
		Name:    restaurant.GetName(),
		Address: restaurant.GetAddress(),
		IsOpen:  restaurant.GetIsOpen(),
	}, nil
}

// MenuItemNames returns no names for a restaurant without a menu: the order
// still shows, with neutral item labels.
func (c *Catalog) MenuItemNames(ctx context.Context, restaurantID string) (map[string]string, error) {
	response, err := c.client.GetMenu(identity.WithoutUser(ctx), &restaurantsv1.GetMenuRequest{RestaurantId: restaurantID})
	if status.Code(err) == codes.NotFound {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get menu: %w", err)
	}
	names := make(map[string]string, len(response.GetItems()))
	for _, item := range response.GetItems() {
		names[item.GetMenuItemId()] = item.GetName()
	}
	return names, nil
}
