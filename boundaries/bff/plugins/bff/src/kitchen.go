package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/kitchen/applications"
)

// QueryKitchen shows the active orders of a restaurant on behalf of the
// verified user; restaurants decides whether the user may operate its kitchen.
// A missing restaurant and a kitchen the user may not operate are both null.
func (s *BffService) QueryKitchen(ctx context.Context, request *service.QueryKitchenRequest) (*service.QueryKitchenResponse, error) {
	if request.GetRestaurantId() == "" {
		return nil, status.Error(codes.InvalidArgument, "restaurantId is required")
	}
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	view, err := s.kitchen.getKitchen.Handle(ctx, request.GetRestaurantId())
	if errors.Is(err, applications.ErrKitchenNotFound) {
		return &service.QueryKitchenResponse{}, nil
	}
	if err != nil {
		return nil, internalError("kitchen", err)
	}
	message, err := kitchenMessage(view)
	if err != nil {
		return nil, internalError("kitchen", err)
	}
	message.RestaurantId = request.GetRestaurantId()
	return &service.QueryKitchenResponse{Kitchen: message}, nil
}

// LookupKitchenByRestaurantId resolves kitchenChanged events (ADR 36) with
// the subscriber's access token: restaurants checks the kitchen right, a
// kitchen the user may not see or a missing one is NOT_FOUND.
func (s *BffService) LookupKitchenByRestaurantId(ctx context.Context, request *service.LookupKitchenByRestaurantIdRequest) (*service.LookupKitchenByRestaurantIdResponse, error) {
	kitchens := make([]*service.Kitchen, 0, len(request.GetKeys()))
	for _, key := range request.GetKeys() {
		response, err := s.QueryKitchen(ctx, &service.QueryKitchenRequest{RestaurantId: key.GetRestaurantId()})
		if err != nil {
			return nil, err
		}
		if response.GetKitchen() == nil {
			return nil, status.Error(codes.NotFound, "kitchen not found")
		}
		kitchens = append(kitchens, response.GetKitchen())
	}
	return &service.LookupKitchenByRestaurantIdResponse{Result: kitchens}, nil
}

// MutationApplyKitchenAction runs a kitchen command on behalf of the verified
// user. A stage conflict is the business problem of the payload; a missing
// order, a missing right and a malformed command are GraphQL errors.
func (s *BffService) MutationApplyKitchenAction(ctx context.Context, request *service.MutationApplyKitchenActionRequest) (*service.MutationApplyKitchenActionResponse, error) {
	input := request.GetInput()
	if input.GetRestaurantId() == "" || input.GetKitchenOrderId() == "" {
		return nil, status.Error(codes.InvalidArgument, "restaurantId and kitchenOrderId are required")
	}
	action, ok := kitchenActions[input.GetAction()]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "action is required")
	}
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.kitchen.applyAction.Handle(ctx, applications.ActionRequest{
		RestaurantID:   input.GetRestaurantId(),
		KitchenOrderID: input.GetKitchenOrderId(),
		Action:         action,
	})
	switch {
	case errors.Is(err, applications.ErrOrderNotFound):
		return nil, status.Error(codes.NotFound, "restaurant order not found")
	case errors.Is(err, applications.ErrNotPermitted):
		return nil, status.Error(codes.PermissionDenied, "kitchen operation is not permitted")
	case errors.Is(err, applications.ErrInvalidCommand):
		return nil, status.Error(codes.InvalidArgument, "kitchen command is invalid")
	case err != nil:
		return nil, internalError("applyKitchenAction", err)
	}
	payload, err := kitchenActionPayload(result)
	if err != nil {
		return nil, internalError("applyKitchenAction", err)
	}
	return &service.MutationApplyKitchenActionResponse{ApplyKitchenAction: payload}, nil
}
