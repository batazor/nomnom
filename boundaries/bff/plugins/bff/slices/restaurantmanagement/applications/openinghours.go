package applications

import (
	"context"
	"fmt"

	catalog "github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/catalog/applications"
)

// InvalidOpeningHours is a weekly schedule restaurants rejected (FR-697):
// Reason is invalid_day, invalid_time, empty_interval or overlap, the interval
// is the offending one as the owner sent it.
type InvalidOpeningHours struct {
	Reason string
	Day    int
	Opens  string
	Closes string
}

func (e InvalidOpeningHours) Error() string {
	return fmt.Sprintf("invalid opening hours: %s at day %d %s-%s", e.Reason, e.Day, e.Opens, e.Closes)
}

// OpeningHoursSetter replaces the weekly opening hours in restaurants on behalf
// of the user in the context. It returns ErrRestaurantNotFound,
// ErrNotPermitted, InvalidOpeningHours or ErrInvalidRestaurant for the refusals
// of restaurants; changed is false when the restaurant already had them.
type OpeningHoursSetter interface {
	SetOpeningHours(ctx context.Context, restaurantID string, hours []catalog.OpeningInterval) (changed bool, err error)
}

// RestaurantViews reads the restaurant as the catalog shows it: the Get
// Restaurant scenario of the catalog.
type RestaurantViews interface {
	Handle(ctx context.Context, restaurantID string) (catalog.RestaurantView, error)
}

// OpeningHoursResult is the restaurant read after the command.
type OpeningHoursResult struct {
	Restaurant catalog.RestaurantView
	Changed    bool
}

// SetOpeningHours replaces the whole weekly schedule of a restaurant and reads
// the restaurant back: whether it accepts orders and when it opens next follow
// the new schedule, which the response of the command does not carry.
type SetOpeningHours struct {
	restaurants OpeningHoursSetter
	views       RestaurantViews
}

func NewSetOpeningHours(restaurants OpeningHoursSetter, views RestaurantViews) SetOpeningHours {
	return SetOpeningHours{restaurants: restaurants, views: views}
}

func (h SetOpeningHours) Handle(ctx context.Context, restaurantID string, hours []catalog.OpeningInterval) (OpeningHoursResult, error) {
	changed, err := h.restaurants.SetOpeningHours(ctx, restaurantID, hours)
	if err != nil {
		return OpeningHoursResult{}, err
	}
	view, err := h.views.Handle(ctx, restaurantID)
	if err != nil {
		// restaurants commits the schedule before it answers.
		return OpeningHoursResult{}, fmt.Errorf("read restaurant %s: %w", restaurantID, err)
	}
	return OpeningHoursResult{Restaurant: view, Changed: changed}, nil
}
