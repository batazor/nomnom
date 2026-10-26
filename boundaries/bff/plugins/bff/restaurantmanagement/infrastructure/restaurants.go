// Package infrastructure adapts the restaurant management ports to the
// OpenRestaurant, CloseRestaurant and ListOwnedRestaurants RPCs of
// restaurants.v1.RestaurantsService, called through the internal gateway with
// the service token of BFF and the user assertion of the verified user
// (ADR 32). restaurants authorizes restaurant#manage itself.
package infrastructure

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/cellrouter"
	restaurantsv1 "github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/grpc/gen/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
)

// Restaurants implements applications.RestaurantOpener.
type Restaurants struct {
	client restaurantsv1.RestaurantsServiceClient
}

func NewRestaurants(conn grpc.ClientConnInterface) *Restaurants {
	return &Restaurants{client: restaurantsv1.NewRestaurantsServiceClient(conn)}
}

var (
	_ applications.RestaurantOpener      = (*Restaurants)(nil)
	_ applications.OwnedRestaurantLister = (*Restaurants)(nil)
)

// ListOwned keeps the user of ctx: restaurants lists only that user's
// restaurants of the Market of the request, so the size is already bounded by
// the scenario.
func (r *Restaurants) ListOwned(ctx context.Context, size int, cursor string) (applications.OwnedPage, error) {
	market, err := cellrouter.MarketFrom(ctx)
	if err != nil {
		return applications.OwnedPage{}, fmt.Errorf("list owned restaurants: %w", err)
	}
	response, err := r.client.ListOwnedRestaurants(ctx, &restaurantsv1.ListOwnedRestaurantsRequest{
		PageSize:  int32(size),
		PageToken: cursor,
		MarketId:  market,
	})
	if err != nil {
		return applications.OwnedPage{}, fmt.Errorf("list owned restaurants: %w", err)
	}
	page := applications.OwnedPage{
		Restaurants: make([]applications.Restaurant, 0, len(response.GetRestaurants())),
		NextCursor:  response.GetNextPageToken(),
	}
	for _, restaurant := range response.GetRestaurants() {
		page.Restaurants = append(page.Restaurants, restaurantOf(restaurant))
	}
	return page, nil
}

// Open keeps the user of ctx: the interceptor turns it into the user assertion.
func (r *Restaurants) Open(ctx context.Context, restaurantID string) (applications.OpeningResult, error) {
	response, err := r.client.OpenRestaurant(ctx, &restaurantsv1.OpenRestaurantRequest{RestaurantId: restaurantID})
	if err != nil {
		return applications.OpeningResult{}, openingError("open restaurant", err)
	}
	return openingResult(response.GetRestaurant(), response.GetChanged()), nil
}

func (r *Restaurants) Close(ctx context.Context, restaurantID string) (applications.OpeningResult, error) {
	response, err := r.client.CloseRestaurant(ctx, &restaurantsv1.CloseRestaurantRequest{RestaurantId: restaurantID})
	if err != nil {
		return applications.OpeningResult{}, openingError("close restaurant", err)
	}
	return openingResult(response.GetRestaurant(), response.GetChanged()), nil
}

// openingError names the refusals of restaurants; UNAVAILABLE and the rest
// stay infrastructure failures.
func openingError(operation string, err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return applications.ErrRestaurantNotFound
	case codes.PermissionDenied:
		return applications.ErrNotPermitted
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %s", applications.ErrInvalidRestaurant, status.Convert(err).Message())
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func openingResult(restaurant *restaurantsv1.Restaurant, changed bool) applications.OpeningResult {
	return applications.OpeningResult{Restaurant: restaurantOf(restaurant), Changed: changed}
}

func restaurantOf(restaurant *restaurantsv1.Restaurant) applications.Restaurant {
	return applications.Restaurant{
		ID:     restaurant.GetRestaurantId(),
		Name:   restaurant.GetName(),
		IsOpen: restaurant.GetIsOpen(),
	}
}
