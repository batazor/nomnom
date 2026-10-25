package applications

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

// MenuReader reads the current menu of a restaurant.
type MenuReader interface {
	// Menu returns ErrMenuNotFound for a restaurant without a menu.
	Menu(ctx context.Context, restaurantID string) ([]MenuItem, error)
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
	menus       MenuReader
}

func NewListRestaurants(restaurants RestaurantLister, menus MenuReader) ListRestaurants {
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

// readMenus reads the menus of a page at once, one GetMenu per restaurant; any
// failure but a missing menu fails the page.
func (h ListRestaurants) readMenus(ctx context.Context, edges []RestaurantEdge) error {
	errs := make([]error, len(edges))
	var wg sync.WaitGroup
	for i := range edges {
		wg.Go(func() {
			menu, err := h.menus.Menu(ctx, edges[i].Restaurant.ID)
			switch {
			case errors.Is(err, ErrMenuNotFound):
			case err != nil:
				errs[i] = fmt.Errorf("read menu of %s: %w", edges[i].Restaurant.ID, err)
			case menu == nil:
				edges[i].Menu = []MenuItem{}
			default:
				edges[i].Menu = menu
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
