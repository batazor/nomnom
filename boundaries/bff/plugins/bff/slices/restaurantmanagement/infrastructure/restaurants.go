// Package infrastructure adapts the restaurant management ports to the
// OpenRestaurant, CloseRestaurant, SetOpeningHours and ListOwnedRestaurants RPCs of
// restaurants.v1.RestaurantsService, called through the internal gateway with
// the service token of BFF and the user assertion of the verified user
// (ADR 32). restaurants authorizes restaurant#manage itself.
package infrastructure

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"buf.build/gen/go/nomnom/restaurants/grpc/go/nomnom/restaurants/v1/restaurantsv1grpc"
	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"
	catalog "github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/catalog/applications"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/market"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/slices/restaurantmanagement/applications"
)

// Restaurants implements applications.RestaurantOpener.
type Restaurants struct {
	client restaurantsv1grpc.RestaurantsServiceClient
}

func NewRestaurants(conn grpc.ClientConnInterface) *Restaurants {
	return &Restaurants{client: restaurantsv1grpc.NewRestaurantsServiceClient(conn)}
}

var (
	_ applications.RestaurantOpener      = (*Restaurants)(nil)
	_ applications.OwnedRestaurantLister = (*Restaurants)(nil)
	_ applications.OpeningHoursSetter    = (*Restaurants)(nil)
)

// ListOwned keeps the user of ctx: restaurants lists only that user's
// restaurants of the Market of the request, so the size is already bounded by
// the scenario.
func (r *Restaurants) ListOwned(ctx context.Context, size int, cursor string) (applications.OwnedPage, error) {
	marketID, err := market.From(ctx)
	if err != nil {
		return applications.OwnedPage{}, fmt.Errorf("list owned restaurants: %w", err)
	}
	response, err := r.client.ListOwnedRestaurants(ctx, &restaurantsv1.ListOwnedRestaurantsRequest{
		PageSize:  int32(size),
		PageToken: cursor,
		MarketId:  marketID,
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

// SetOpeningHours keeps the user of ctx: restaurants checks restaurant#manage.
func (r *Restaurants) SetOpeningHours(ctx context.Context, restaurantID string, hours []catalog.OpeningInterval) (bool, error) {
	request := &restaurantsv1.SetOpeningHoursRequest{
		RestaurantId: restaurantID,
		OpeningHours: make([]*restaurantsv1.OpeningInterval, 0, len(hours)),
	}
	for _, interval := range hours {
		request.OpeningHours = append(request.OpeningHours, &restaurantsv1.OpeningInterval{
			DayOfWeek: int32(interval.Day), // 1..7 is checked by restaurants
			Opens:     interval.Opens,
			Closes:    interval.Closes,
		})
	}
	response, err := r.client.SetOpeningHours(ctx, request)
	if err != nil {
		if invalid, ok := invalidOpeningHours(err); ok {
			return false, invalid
		}
		return false, openingError("set opening hours", err)
	}
	return response.GetChanged(), nil
}

// invalidHoursMessage is the text of InvalidOpeningHoursError of restaurants:
// INVALID_ARGUMENT of SetOpeningHours carries no ErrorInfo, the day and the
// interval are only in the message.
var invalidHoursMessage = regexp.MustCompile(`^invalid opening hours: (invalid_day|invalid_time|empty_interval|overlap) at day (-?\d+) (\S*)-(\S*)$`)

// invalidOpeningHours names the offending interval of a rejected schedule.
func invalidOpeningHours(err error) (applications.InvalidOpeningHours, bool) {
	st := status.Convert(err)
	if st.Code() != codes.InvalidArgument {
		return applications.InvalidOpeningHours{}, false
	}
	match := invalidHoursMessage.FindStringSubmatch(st.Message())
	if match == nil {
		return applications.InvalidOpeningHours{}, false
	}
	day, err := strconv.Atoi(match[2])
	if err != nil {
		return applications.InvalidOpeningHours{}, false
	}
	return applications.InvalidOpeningHours{Reason: match[1], Day: day, Opens: match[3], Closes: match[4]}, true
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
