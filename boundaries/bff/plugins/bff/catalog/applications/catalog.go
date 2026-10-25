package applications

import (
	"context"
	"errors"
	"fmt"
)

// RestaurantEdge is one restaurant of a page with its opaque cursor.
type RestaurantEdge struct {
	Cursor     string
	Restaurant Restaurant
}

// RestaurantPage is a page of the restaurants connection.
type RestaurantPage struct {
	Edges       []RestaurantEdge
	HasNextPage bool
	// EndCursor is the cursor of the last edge, empty for an empty page.
	EndCursor string
}

// ListRestaurants pages the restaurants open now; closed ones are not offered
// to the customer.
type ListRestaurants struct {
	restaurants Restaurants
}

func NewListRestaurants(restaurants Restaurants) ListRestaurants {
	return ListRestaurants{restaurants: restaurants}
}

// Handle passes the cursor of restaurants through as is: next_page_token is
// the id of the last restaurant of the page, so it also serves as the cursor
// of every edge.
func (h ListRestaurants) Handle(ctx context.Context, first int, after string) (RestaurantPage, error) {
	if first < 1 || first > MaxPageSize {
		return RestaurantPage{}, ErrInvalidPageSize
	}
	page, err := h.restaurants.List(ctx, PageRequest{Size: first, Cursor: after})
	if err != nil {
		return RestaurantPage{}, err
	}
	result := RestaurantPage{
		Edges:       make([]RestaurantEdge, 0, len(page.Restaurants)),
		HasNextPage: page.NextCursor != "",
		EndCursor:   page.NextCursor,
	}
	for _, restaurant := range page.Restaurants {
		result.Edges = append(result.Edges, RestaurantEdge{Cursor: restaurant.ID, Restaurant: restaurant})
	}
	if result.EndCursor == "" && len(result.Edges) > 0 {
		result.EndCursor = result.Edges[len(result.Edges)-1].Cursor
	}
	return result, nil
}

// RestaurantView is a restaurant with its current menu.
type RestaurantView struct {
	Restaurant Restaurant
	// Menu is nil when the restaurant has not published one.
	Menu []MenuItem
}

// GetRestaurant reads a restaurant and its menu.
type GetRestaurant struct {
	restaurants Restaurants
}

func NewGetRestaurant(restaurants Restaurants) GetRestaurant {
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
