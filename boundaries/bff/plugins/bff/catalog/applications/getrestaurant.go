package applications

import (
	"context"
	"errors"
	"fmt"
)

// RestaurantReader reads one restaurant and its menu.
type RestaurantReader interface {
	// Restaurant returns ErrRestaurantNotFound for an unknown restaurant.
	Restaurant(ctx context.Context, restaurantID string) (Restaurant, error)
	// Menu returns ErrMenuNotFound for a restaurant without a menu.
	Menu(ctx context.Context, restaurantID string) ([]MenuItem, error)
}

// RestaurantView is a restaurant with its current menu.
type RestaurantView struct {
	Restaurant Restaurant
	// Menu is nil when the restaurant has not published one.
	Menu []MenuItem
}

// GetRestaurant reads a restaurant and its menu.
type GetRestaurant struct {
	restaurants RestaurantReader
}

func NewGetRestaurant(restaurants RestaurantReader) GetRestaurant {
	return GetRestaurant{restaurants: restaurants}
}

// Handle returns ErrRestaurantNotFound for an unknown restaurant; a restaurant
// without a menu has a nil Menu.
func (h GetRestaurant) Handle(ctx context.Context, restaurantID string) (RestaurantView, error) {
	restaurant, err := h.restaurants.Restaurant(ctx, restaurantID)
	if err != nil {
		return RestaurantView{}, err
	}
	menu, err := h.restaurants.Menu(ctx, restaurantID)
	if errors.Is(err, ErrMenuNotFound) {
		return RestaurantView{Restaurant: restaurant}, nil
	}
	if err != nil {
		return RestaurantView{}, fmt.Errorf("read menu: %w", err)
	}
	if menu == nil {
		menu = []MenuItem{}
	}
	return RestaurantView{Restaurant: restaurant, Menu: menu}, nil
}
