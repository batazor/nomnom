package applications

import "context"

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
	restaurants RestaurantLister
}

func NewListRestaurants(restaurants RestaurantLister) ListRestaurants {
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
