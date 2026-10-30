package applications

import (
	"context"
	"errors"
)

// ErrInvalidPageSize is a page size outside 1..MaxPageSize.
var ErrInvalidPageSize = errors.New("page size must be between 1 and 100")

// MaxPageSize is the largest page restaurants serves.
const MaxPageSize = 100

// OwnedPage is one page of restaurants as restaurants returns it; NextCursor
// is empty on the last page.
type OwnedPage struct {
	Restaurants []Restaurant
	NextCursor  string
}

// OwnedRestaurantLister pages the restaurants owned by the user in the context.
type OwnedRestaurantLister interface {
	ListOwned(ctx context.Context, size int, cursor string) (OwnedPage, error)
}

// RestaurantEdge is a restaurant with its cursor.
type RestaurantEdge struct {
	Cursor     string
	Restaurant Restaurant
}

// RestaurantPage is a page of the owner's restaurants connection.
type RestaurantPage struct {
	Edges       []RestaurantEdge
	HasNextPage bool
	// EndCursor is the cursor of the last edge, empty for an empty page.
	EndCursor string
}

// ListOwnedRestaurants pages the open and closed restaurants of the owner;
// restaurants filters them by the verified user.
type ListOwnedRestaurants struct {
	restaurants OwnedRestaurantLister
}

func NewListOwnedRestaurants(restaurants OwnedRestaurantLister) ListOwnedRestaurants {
	return ListOwnedRestaurants{restaurants: restaurants}
}

// Handle passes the cursor of restaurants through: next_page_token is the id
// of the last restaurant of the page, so it is also the cursor of every edge.
func (h ListOwnedRestaurants) Handle(ctx context.Context, first int, after string) (RestaurantPage, error) {
	if first < 1 || first > MaxPageSize {
		return RestaurantPage{}, ErrInvalidPageSize
	}
	page, err := h.restaurants.ListOwned(ctx, first, after)
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
	return result, nil
}
