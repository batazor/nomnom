package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/catalog/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

// QueryRestaurants is public: no access token is required (FR-499).
func (s *BffService) QueryRestaurants(ctx context.Context, request *service.QueryRestaurantsRequest) (*service.QueryRestaurantsResponse, error) {
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
	view, err := s.catalog.getRestaurant.Handle(ctx, request.GetId())
	if errors.Is(err, applications.ErrRestaurantNotFound) {
		return &service.QueryRestaurantResponse{}, nil
	}
	if err != nil {
		return nil, internalError("restaurant", err)
	}
	return &service.QueryRestaurantResponse{Restaurant: restaurantWithMenu(view)}, nil
}

// ResolveRestaurantScheduledSlots resolves Restaurant.scheduledSlots of every
// restaurant of the response in the order of the context; public like the
// catalog. Only a query that asks for the field reads the windows.
func (s *BffService) ResolveRestaurantScheduledSlots(ctx context.Context, request *service.ResolveRestaurantScheduledSlotsRequest) (*service.ResolveRestaurantScheduledSlotsResponse, error) {
	restaurantIDs := make([]string, 0, len(request.GetContext()))
	for _, restaurant := range request.GetContext() {
		restaurantIDs = append(restaurantIDs, restaurant.GetId())
	}
	slots, err := s.catalog.listScheduledSlots.Handle(ctx, restaurantIDs)
	if err != nil {
		return nil, internalError("scheduledSlots", err)
	}
	response := &service.ResolveRestaurantScheduledSlotsResponse{Result: make([]*service.ResolveRestaurantScheduledSlotsResult, 0, len(slots))}
	for _, windows := range slots {
		result := &service.ResolveRestaurantScheduledSlotsResult{ScheduledSlots: make([]*service.DeliveryWindow, 0, len(windows))}
		for _, slot := range windows {
			result.ScheduledSlots = append(result.ScheduledSlots, deliveryWindowMessage(slot))
		}
		response.Result = append(response.Result, result)
	}
	return response, nil
}
