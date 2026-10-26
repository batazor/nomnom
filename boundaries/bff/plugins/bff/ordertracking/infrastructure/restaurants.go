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
	restaurant := response.GetRestaurant()
	return applications.Restaurant{
		ID:     restaurant.GetRestaurantId(),
		Name:   restaurant.GetName(),
		IsOpen: restaurant.GetIsOpen(),
	}, nil
}
