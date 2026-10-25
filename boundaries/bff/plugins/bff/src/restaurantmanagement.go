package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/restaurantmanagement/applications"
)

// restaurantManagementSlice names the slice in "not available" errors.
const restaurantManagementSlice = "restaurant management"

type openingHandler interface {
	Handle(ctx context.Context, restaurantID string) (applications.OpeningResult, error)
}

type ownedRestaurantsHandler interface {
	Handle(ctx context.Context, first int, after string) (applications.RestaurantPage, error)
}

// QueryMyRestaurants lists the restaurants of the verified user; restaurants
// filters them by owner, so a user without restaurants gets an empty page.
func (s *BffService) QueryMyRestaurants(ctx context.Context, request *service.QueryMyRestaurantsRequest) (*service.QueryMyRestaurantsResponse, error) {
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireSlice(restaurantManagementSlice, s.management != nil); err != nil {
		return nil, err
	}
	page, err := s.management.listOwnedRestaurants.Handle(ctx, int(request.GetFirst()), request.GetAfter().GetValue())
	if errors.Is(err, applications.ErrInvalidPageSize) {
		return nil, status.Error(codes.InvalidArgument, "first must be between 1 and 100")
	}
	if err != nil {
		return nil, internalError("myRestaurants", err)
	}
	return &service.QueryMyRestaurantsResponse{MyRestaurants: ownedRestaurantConnection(page)}, nil
}

// MutationOpenRestaurant opens the restaurant on behalf of the verified user;
// restaurants decides whether the user may manage it (FR-601).
func (s *BffService) MutationOpenRestaurant(ctx context.Context, request *service.MutationOpenRestaurantRequest) (*service.MutationOpenRestaurantResponse, error) {
	payload, err := s.changeOpening(ctx, "openRestaurant", request.GetRestaurantId(), func(m *restaurantManagement) openingHandler { return m.openRestaurant })
	if err != nil {
		return nil, err
	}
	return &service.MutationOpenRestaurantResponse{OpenRestaurant: payload}, nil
}

// MutationCloseRestaurant closes the restaurant on behalf of the verified user.
func (s *BffService) MutationCloseRestaurant(ctx context.Context, request *service.MutationCloseRestaurantRequest) (*service.MutationCloseRestaurantResponse, error) {
	payload, err := s.changeOpening(ctx, "closeRestaurant", request.GetRestaurantId(), func(m *restaurantManagement) openingHandler { return m.closeRestaurant })
	if err != nil {
		return nil, err
	}
	return &service.MutationCloseRestaurantResponse{CloseRestaurant: payload}, nil
}

// changeOpening authenticates the user and translates the refusals of
// restaurants into GraphQL errors: the schema has no business problem for
// opening, every refusal is a missing restaurant, a missing right or a
// malformed id.
func (s *BffService) changeOpening(ctx context.Context, operation, restaurantID string, handler func(*restaurantManagement) openingHandler) (*service.RestaurantOpeningPayload, error) {
	if restaurantID == "" {
		return nil, status.Error(codes.InvalidArgument, "restaurantId is required")
	}
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireSlice(restaurantManagementSlice, s.management != nil); err != nil {
		return nil, err
	}
	result, err := handler(s.management).Handle(ctx, restaurantID)
	switch {
	case errors.Is(err, applications.ErrRestaurantNotFound):
		return nil, status.Error(codes.NotFound, "restaurant not found")
	case errors.Is(err, applications.ErrNotPermitted):
		return nil, status.Error(codes.PermissionDenied, "restaurant management is not permitted")
	case errors.Is(err, applications.ErrInvalidRestaurant):
		return nil, status.Error(codes.InvalidArgument, "restaurantId is invalid")
	case err != nil:
		return nil, internalError(operation, err)
	}
	return restaurantOpeningPayload(result), nil
}
