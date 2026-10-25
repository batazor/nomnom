package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/checkout/applications"
	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
)

// MutationPlaceOrder places the Order on behalf of the verified customer;
// business refusals are payload problems, not GraphQL errors (FR-500).
func (s *BffService) MutationPlaceOrder(ctx context.Context, request *service.MutationPlaceOrderRequest) (*service.MutationPlaceOrderResponse, error) {
	input := request.GetInput()
	if input.GetRestaurantId() == "" || input.GetIdempotencyKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "restaurantId and idempotencyKey are required")
	}
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	lines := make([]applications.Line, 0, len(input.GetItems()))
	for _, item := range input.GetItems() {
		lines = append(lines, applications.Line{MenuItemID: item.GetMenuItemId(), Quantity: int(item.GetQuantity())})
	}
	result, err := s.checkout.placeOrder.Handle(ctx, applications.Input{
		RestaurantID:    input.GetRestaurantId(),
		Lines:           lines,
		DeliveryAddress: input.GetDeliveryAddress(),
		IdempotencyKey:  input.GetIdempotencyKey(),
	})
	switch {
	case errors.Is(err, applications.ErrRestaurantNotFound):
		// The schema has no problem for it: the client offered a restaurant
		// that does not exist, which is not a fixable cart problem.
		return nil, status.Error(codes.NotFound, "restaurant not found")
	case errors.Is(err, applications.ErrInvalidOrder):
		return nil, status.Error(codes.InvalidArgument, "order input is invalid")
	case err != nil:
		return nil, internalError("placeOrder", err)
	}
	payload, err := placeOrderPayload(result)
	if err != nil {
		return nil, internalError("placeOrder", err)
	}
	return &service.MutationPlaceOrderResponse{PlaceOrder: payload}, nil
}
