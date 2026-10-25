package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

// catalogSlice names the slice in "not available" errors.
const catalogSlice = "catalog"

// QueryRestaurants is public: no access token is required (FR-499).
func (s *BffService) QueryRestaurants(ctx context.Context, request *service.QueryRestaurantsRequest) (*service.QueryRestaurantsResponse, error) {
	if err := requireSlice(catalogSlice, s.catalog != nil); err != nil {
		return nil, err
	}
	page, err := s.catalog.listRestaurants.Handle(ctx, int(request.GetFirst()), request.GetAfter().GetValue())
	if errors.Is(err, applications.ErrInvalidPageSize) {
		return nil, status.Error(codes.InvalidArgument, "first must be between 1 and 100")
	}
	if err != nil {
		return nil, internalError("restaurants", err)
	}
	return &service.QueryRestaurantsResponse{Restaurants: restaurantConnection(page)}, nil
}

// QueryRestaurant is public; an unknown restaurant is null.
func (s *BffService) QueryRestaurant(ctx context.Context, request *service.QueryRestaurantRequest) (*service.QueryRestaurantResponse, error) {
	if err := requireSlice(catalogSlice, s.catalog != nil); err != nil {
		return nil, err
	}
	view, err := s.catalog.getRestaurant.Handle(ctx, request.GetId())
	if errors.Is(err, applications.ErrRestaurantNotFound) {
		return &service.QueryRestaurantResponse{}, nil
	}
	if err != nil {
		return nil, internalError("restaurant", err)
	}
	return &service.QueryRestaurantResponse{Restaurant: restaurantWithMenu(view)}, nil
}
