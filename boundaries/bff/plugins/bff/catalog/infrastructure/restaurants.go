// Package infrastructure adapts the catalog ports to the public reads of
// restaurants.v1.RestaurantsService, called through the internal gateway with
// the service token of BFF and without a user assertion.
package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
)

// Restaurants implements applications.RestaurantLister and
// applications.RestaurantReader.
type Restaurants struct {
	client restaurantsv1.RestaurantsServiceClient
}

func NewRestaurants(conn grpc.ClientConnInterface) *Restaurants {
	return &Restaurants{client: restaurantsv1.NewRestaurantsServiceClient(conn)}
}

var (
	_ applications.RestaurantLister = (*Restaurants)(nil)
	_ applications.RestaurantReader = (*Restaurants)(nil)
)

// List asks for open restaurants only: include_closed stays false.
func (r *Restaurants) List(ctx context.Context, request applications.PageRequest) (applications.Page, error) {
	response, err := r.client.ListRestaurants(identity.WithoutUser(ctx), &restaurantsv1.ListRestaurantsRequest{
		PageSize:  int32(request.Size), // bounded by MaxPageSize in the scenario
		PageToken: request.Cursor,
	})
	if err != nil {
		return applications.Page{}, fmt.Errorf("list restaurants: %w", err)
	}
	page := applications.Page{
		Restaurants: make([]applications.Restaurant, 0, len(response.GetRestaurants())),
		NextCursor:  response.GetNextPageToken(),
	}
	for _, restaurant := range response.GetRestaurants() {
		page.Restaurants = append(page.Restaurants, restaurantOf(restaurant))
	}
	return page, nil
}

func (r *Restaurants) Restaurant(ctx context.Context, restaurantID string) (applications.Restaurant, error) {
	response, err := r.client.GetRestaurant(identity.WithoutUser(ctx), &restaurantsv1.GetRestaurantRequest{RestaurantId: restaurantID})
	if status.Code(err) == codes.NotFound {
		return applications.Restaurant{}, applications.ErrRestaurantNotFound
	}
	if err != nil {
		return applications.Restaurant{}, fmt.Errorf("get restaurant: %w", err)
	}
	return restaurantOf(response.GetRestaurant()), nil
}

func (r *Restaurants) Menu(ctx context.Context, restaurantID string) ([]applications.MenuItem, error) {
	response, err := r.client.GetMenu(identity.WithoutUser(ctx), &restaurantsv1.GetMenuRequest{RestaurantId: restaurantID})
	if status.Code(err) == codes.NotFound {
		return nil, applications.ErrMenuNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get menu: %w", err)
	}
	items := make([]applications.MenuItem, 0, len(response.GetItems()))
	for _, item := range response.GetItems() {
		items = append(items, applications.MenuItem{
			ID:          item.GetMenuItemId(),
			Name:        item.GetName(),
			Description: item.GetDescription(),
			Price:       applications.Money{Amount: item.GetPrice().GetAmount(), Currency: item.GetPrice().GetCurrency()},
			Available:   item.GetAvailable(),
		})
	}
	return items, nil
}

func restaurantOf(restaurant *restaurantsv1.Restaurant) applications.Restaurant {
	return applications.Restaurant{
		ID:     restaurant.GetRestaurantId(),
		Name:   restaurant.GetName(),
		IsOpen: restaurant.GetIsOpen(),
	}
}
