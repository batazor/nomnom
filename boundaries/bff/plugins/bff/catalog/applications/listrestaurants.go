package applications

import (
	"context"
	"fmt"
)

// RestaurantLister pages the public list of restaurants.
type RestaurantLister interface {
	// List returns open restaurants ordered by a stable key.
	List(ctx context.Context, request PageRequest) (Page, error)
}

// PageRequest asks for Size restaurants after the Cursor of a previous page.
type PageRequest struct {
	Size   int
	Cursor string
}

// Page is one page of restaurants; NextCursor is empty on the last page.
type Page struct {
	Restaurants []Restaurant
	NextCursor  string
}

// MenuBatchReader reads the current menus of the restaurants of a page in one
// call (ADR 7).
type MenuBatchReader interface {
	// Menus returns the menus of the restaurants that have one, by restaurant
	// id; a restaurant without a menu is absent.
	Menus(ctx context.Context, restaurantIDs []string) (map[string][]MenuItem, error)
}

// RestaurantEdge is one restaurant of a page with its opaque cursor and menu.
type RestaurantEdge struct {
	Cursor     string
	Restaurant Restaurant
	// Menu is nil when the restaurant has not published one.
	Menu []MenuItem
}

// RestaurantPage is a page of the restaurants connection.
type RestaurantPage struct {
	Edges       []RestaurantEdge
	HasNextPage bool
	// EndCursor is the cursor of the last edge, empty for an empty page.
	EndCursor string
}

// ListRestaurants pages the restaurants open now with their menus; closed ones
// are not offered to the customer. The menus let the customer UI preview the
// dishes on each restaurant card.
type ListRestaurants struct {
	restaurants RestaurantLister
	menus       MenuBatchReader
}

func NewListRestaurants(restaurants RestaurantLister, menus MenuBatchReader) ListRestaurants {
	return ListRestaurants{restaurants: restaurants, menus: menus}
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
	if err := h.readMenus(ctx, result.Edges); err != nil {
		return RestaurantPage{}, err
	}
	if result.EndCursor == "" && len(result.Edges) > 0 {
		result.EndCursor = result.Edges[len(result.Edges)-1].Cursor
	}
	return result, nil
}

// readMenus reads the menus of a page in one call to restaurants; a
// restaurant without a menu keeps a nil Menu.
func (h ListRestaurants) readMenus(ctx context.Context, edges []RestaurantEdge) error {
	if len(edges) == 0 {
		return nil
	}
	ids := make([]string, len(edges))
	for i, edge := range edges {
		ids[i] = edge.Restaurant.ID
	}
	menus, err := h.menus.Menus(ctx, ids)
	if err != nil {
		return fmt.Errorf("read menus: %w", err)
	}
	for i := range edges {
		edges[i].Menu = menus[edges[i].Restaurant.ID]
	}
	return nil
}
