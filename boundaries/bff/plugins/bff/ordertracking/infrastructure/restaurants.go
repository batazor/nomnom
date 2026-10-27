package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

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
	return restaurantOf(response.GetRestaurant()), nil
}

// Restaurants reads the restaurants of a page with one BatchGetRestaurants.
func (c *Catalog) Restaurants(ctx context.Context, restaurantIDs []string) (map[string]applications.Restaurant, error) {
	response, err := c.client.BatchGetRestaurants(identity.WithoutUser(ctx), &restaurantsv1.BatchGetRestaurantsRequest{RestaurantIds: restaurantIDs})
	if err != nil {
		return nil, fmt.Errorf("batch get restaurants: %w", err)
	}
	restaurants := make(map[string]applications.Restaurant, len(response.GetRestaurants()))
	for _, restaurant := range response.GetRestaurants() {
		restaurants[restaurant.GetRestaurantId()] = restaurantOf(restaurant)
	}
	return restaurants, nil
}

func restaurantOf(restaurant *restaurantsv1.Restaurant) applications.Restaurant {
	return applications.Restaurant{
		ID:     restaurant.GetRestaurantId(),
		Name:   restaurant.GetName(),
		IsOpen: restaurant.GetIsOpen(),
	}
}
