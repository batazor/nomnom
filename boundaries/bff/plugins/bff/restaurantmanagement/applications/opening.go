// Package applications holds the restaurant management scenarios of the owner
// screen: opening and closing a restaurant for new orders. restaurants
// authorizes the user (restaurant#manage); BFF only forwards the verified user.
// Protobuf and GraphQL types do not reach it (BFF ADR 2).
package applications

import (
	"context"
	"errors"
)

var (
	// ErrRestaurantNotFound is a restaurant unknown to restaurants.
	ErrRestaurantNotFound = errors.New("restaurant not found")
	// ErrNotPermitted is a user who may not manage the restaurant.
	ErrNotPermitted = errors.New("restaurant management is not permitted")
	// ErrInvalidRestaurant is a restaurant id restaurants rejects as malformed.
	ErrInvalidRestaurant = errors.New("restaurant id is invalid")
)

// Restaurant is the restaurant after an opening command.
type Restaurant struct {
	ID     string
	Name   string
	IsOpen bool
}

// OpeningResult is the restaurant after the command; Changed is false when
// it already was in the requested state.
type OpeningResult struct {
	Restaurant Restaurant
	Changed    bool
}

// RestaurantOpener runs the opening commands of restaurants on behalf of the
// user in the context. Both return ErrRestaurantNotFound, ErrNotPermitted or
// ErrInvalidRestaurant for the refusals of restaurants.
type RestaurantOpener interface {
	Open(ctx context.Context, restaurantID string) (OpeningResult, error)
	Close(ctx context.Context, restaurantID string) (OpeningResult, error)
}

// OpenRestaurant opens a restaurant for new orders.
type OpenRestaurant struct {
	restaurants RestaurantOpener
}

func NewOpenRestaurant(restaurants RestaurantOpener) OpenRestaurant {
	return OpenRestaurant{restaurants: restaurants}
}

// Handle is idempotent: a repeat answers Changed = false.
func (h OpenRestaurant) Handle(ctx context.Context, restaurantID string) (OpeningResult, error) {
	return h.restaurants.Open(ctx, restaurantID)
}

// CloseRestaurant closes a restaurant for new orders.
type CloseRestaurant struct {
	restaurants RestaurantOpener
}

func NewCloseRestaurant(restaurants RestaurantOpener) CloseRestaurant {
	return CloseRestaurant{restaurants: restaurants}
}

// Handle is idempotent: a repeat answers Changed = false.
func (h CloseRestaurant) Handle(ctx context.Context, restaurantID string) (OpeningResult, error) {
	return h.restaurants.Close(ctx, restaurantID)
}
