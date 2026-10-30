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

	"buf.build/gen/go/nomnom/restaurants/grpc/go/nomnom/restaurants/v1/restaurantsv1grpc"
	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/catalog/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/market"
)

// Restaurants implements applications.RestaurantLister,
// applications.RestaurantReader, applications.MenuBatchReader and
// applications.SlotReader.
type Restaurants struct {
	client restaurantsv1grpc.RestaurantsServiceClient
}

func NewRestaurants(conn grpc.ClientConnInterface) *Restaurants {
	return &Restaurants{client: restaurantsv1grpc.NewRestaurantsServiceClient(conn)}
}

var (
	_ applications.RestaurantLister = (*Restaurants)(nil)
	_ applications.RestaurantReader = (*Restaurants)(nil)
	_ applications.MenuBatchReader  = (*Restaurants)(nil)
	_ applications.SlotReader       = (*Restaurants)(nil)
)

// List asks for open restaurants of the Market of the request only:
// include_closed stays false.
func (r *Restaurants) List(ctx context.Context, request applications.PageRequest) (applications.Page, error) {
	marketID, err := market.From(ctx)
	if err != nil {
		return applications.Page{}, fmt.Errorf("list restaurants: %w", err)
	}
	response, err := r.client.ListRestaurants(identity.WithoutUser(ctx), &restaurantsv1.ListRestaurantsRequest{
		PageSize:  int32(request.Size), // bounded by MaxPageSize in the scenario
		PageToken: request.Cursor,
		MarketId:  marketID,
	})
	if err != nil {
		return applications.Page{}, fmt.Errorf("list restaurants: %w", err)
	}
	page := applications.Page{
		Restaurants: make([]applications.Restaurant, 0, len(response.GetRestaurants())),
		NextCursor:  response.GetNextPageToken(),
	}
	for _, restaurant := range response.GetRestaurants() {
		view, err := restaurantOf(restaurant)
		if err != nil {
			return applications.Page{}, fmt.Errorf("list restaurants: %w", err)
		}
		page.Restaurants = append(page.Restaurants, view)
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
	view, err := restaurantOf(response.GetRestaurant())
	if err != nil {
		return applications.Restaurant{}, fmt.Errorf("get restaurant: %w", err)
	}
	return view, nil
}

func (r *Restaurants) Menu(ctx context.Context, restaurantID string) ([]applications.MenuItem, error) {
	response, err := r.client.GetMenu(identity.WithoutUser(ctx), &restaurantsv1.GetMenuRequest{RestaurantId: restaurantID})
	if status.Code(err) == codes.NotFound {
		return nil, applications.ErrMenuNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get menu: %w", err)
	}
	return menuItemsOf(response.GetItems()), nil
}

// Menus reads the menus of a page with one BatchGetMenus; a published menu
// without items is an empty, not a nil, slice.
func (r *Restaurants) Menus(ctx context.Context, restaurantIDs []string) (map[string][]applications.MenuItem, error) {
	response, err := r.client.BatchGetMenus(identity.WithoutUser(ctx), &restaurantsv1.BatchGetMenusRequest{RestaurantIds: restaurantIDs})
	if err != nil {
		return nil, fmt.Errorf("batch get menus: %w", err)
	}
	menus := make(map[string][]applications.MenuItem, len(response.GetMenus()))
	for _, menu := range response.GetMenus() {
		menus[menu.GetRestaurantId()] = menuItemsOf(menu.GetItems())
	}
	return menus, nil
}

// ScheduledSlots reads the public ListScheduledSlots without the user. A
// window without start or end is an error, not a window at the epoch.
func (r *Restaurants) ScheduledSlots(ctx context.Context, restaurantID string) ([]applications.Slot, error) {
	response, err := r.client.ListScheduledSlots(identity.WithoutUser(ctx), &restaurantsv1.ListScheduledSlotsRequest{RestaurantId: restaurantID})
	if err != nil {
		return nil, fmt.Errorf("list scheduled slots: %w", err)
	}
	slots := make([]applications.Slot, 0, len(response.GetSlots()))
	for _, slot := range response.GetSlots() {
		window := slot.GetWindow()
		if window.GetStart() == nil || window.GetEnd() == nil {
			return nil, fmt.Errorf("list scheduled slots: restaurant %s has a window without bounds", restaurantID)
		}
		slots = append(slots, applications.Slot{
			Start:     window.GetStart().AsTime(),
			End:       window.GetEnd().AsTime(),
			Available: slot.GetAvailable(),
		})
	}
	return slots, nil
}

func menuItemsOf(items []*restaurantsv1.MenuItem) []applications.MenuItem {
	result := make([]applications.MenuItem, 0, len(items))
	for _, item := range items {
		result = append(result, applications.MenuItem{
			ID:          item.GetMenuItemId(),
			Name:        item.GetName(),
			Description: item.GetDescription(),
			Price:       applications.Money{Amount: item.GetPrice().GetAmount(), Currency: item.GetPrice().GetCurrency()},
			Available:   item.GetAvailable(),
		})
	}
	return result
}

// restaurantOf requires wait_time and time_zone: GetRestaurant and
// ListRestaurants always set them (restaurants FR-201, FR-204).
// acceptingOrders is accepting_orders — the flag and the opening hours at the
// read — not the flag is_open alone (FR-699).
func restaurantOf(restaurant *restaurantsv1.Restaurant) (applications.Restaurant, error) {
	waitTime := restaurant.GetWaitTime()
	if waitTime == nil {
		return applications.Restaurant{}, fmt.Errorf("restaurant %s has no wait_time", restaurant.GetRestaurantId())
	}
	if restaurant.GetTimeZone() == "" {
		return applications.Restaurant{}, fmt.Errorf("restaurant %s has no time_zone", restaurant.GetRestaurantId())
	}
	view := applications.Restaurant{
		ID:              restaurant.GetRestaurantId(),
		Name:            restaurant.GetName(),
		AcceptingOrders: restaurant.GetAcceptingOrders(),
		WaitTime: applications.WaitTime{
			Elevated:            waitTime.GetElevated(),
			ExpectedWaitMinutes: int(waitTime.GetExpectedWaitMinutes()),
		},
		TimeZone:     restaurant.GetTimeZone(),
		OpeningHours: make([]applications.OpeningInterval, 0, len(restaurant.GetOpeningHours())),
	}
	for _, interval := range restaurant.GetOpeningHours() {
		view.OpeningHours = append(view.OpeningHours, applications.OpeningInterval{
			Day:    int(interval.GetDayOfWeek()),
			Opens:  interval.GetOpens(),
			Closes: interval.GetCloses(),
		})
	}
	if next := restaurant.GetNextOpeningAt(); next != nil {
		at := next.AsTime()
		view.NextOpeningAt = &at
	}
	if fee := restaurant.GetPremiumFee(); fee != nil {
		view.PremiumFee = &applications.Money{Amount: fee.GetAmount(), Currency: fee.GetCurrency()}
	}
	return view, nil
}
