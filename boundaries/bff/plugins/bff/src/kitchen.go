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

// MutationStartNextPreparation starts the head of the Kitchen Queue on behalf
// of the verified user. An empty queue is a payload without order; no free
// cook is the problem NO_COOK_AVAILABLE.
func (s *BffService) MutationStartNextPreparation(ctx context.Context, request *service.MutationStartNextPreparationRequest) (*service.MutationStartNextPreparationResponse, error) {
	if request.GetRestaurantId() == "" || request.GetRequestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "restaurantId and requestId are required")
	}
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.kitchen.startNext.Handle(ctx, applications.StartNextRequest{
		RestaurantID: request.GetRestaurantId(),
		RequestID:    request.GetRequestId(),
	})
	switch {
	case errors.Is(err, applications.ErrOrderNotFound):
		return nil, status.Error(codes.NotFound, "restaurant not found")
	case errors.Is(err, applications.ErrNotPermitted):
		return nil, status.Error(codes.PermissionDenied, "kitchen operation is not permitted")
	case errors.Is(err, applications.ErrInvalidCommand):
		return nil, status.Error(codes.InvalidArgument, "kitchen command is invalid")
	case err != nil:
		return nil, internalError("startNextPreparation", err)
	}
	payload, err := kitchenActionPayload(result)
	if err != nil {
		return nil, internalError("startNextPreparation", err)
	}
	return &service.MutationStartNextPreparationResponse{StartNextPreparation: payload}, nil
}

// changeCookPool runs a Cook Pool command on behalf of the verified user:
// business refusals are problems of the payload; a missing cook, a missing
// right and an unknown restaurant are GraphQL errors.
func (s *BffService) changeCookPool(ctx context.Context, operation string, request applications.CookPoolRequest) (*service.CookPoolPayload, error) {
	if request.RestaurantID == "" {
		return nil, status.Error(codes.InvalidArgument, "restaurantId is required")
	}
	if request.Command != applications.CookCommandHire && request.CookID == "" {
		return nil, status.Error(codes.InvalidArgument, "cookId is required")
	}
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.kitchen.changeCookPool.Handle(ctx, request)
	switch {
	case errors.Is(err, applications.ErrCookNotFound), errors.Is(err, applications.ErrKitchenNotFound):
		return nil, status.Error(codes.NotFound, "cook not found")
	case errors.Is(err, applications.ErrNotPermitted):
		return nil, status.Error(codes.PermissionDenied, "cook pool operation is not permitted")
	case err != nil:
		return nil, internalError(operation, err)
	}
	payload, err := cookPoolPayload(result)
	if err != nil {
		return nil, internalError(operation, err)
	}
	return payload, nil
}

// MutationHireCook adds a cook off line to the Cook Pool (FR-625).
func (s *BffService) MutationHireCook(ctx context.Context, request *service.MutationHireCookRequest) (*service.MutationHireCookResponse, error) {
	payload, err := s.changeCookPool(ctx, "hireCook", applications.CookPoolRequest{
		RestaurantID: request.GetRestaurantId(),
		Command:      applications.CookCommandHire,
		Name:         request.GetName(),
	})
	if err != nil {
		return nil, err
	}
	return &service.MutationHireCookResponse{HireCook: payload}, nil
}

// MutationSetCookOnLine puts a cook on line or takes them off line.
func (s *BffService) MutationSetCookOnLine(ctx context.Context, request *service.MutationSetCookOnLineRequest) (*service.MutationSetCookOnLineResponse, error) {
	command := applications.CookCommandTakeOffLine
	if request.GetOnLine() {
		command = applications.CookCommandPutOnLine
	}
	payload, err := s.changeCookPool(ctx, "setCookOnLine", applications.CookPoolRequest{
		RestaurantID: request.GetRestaurantId(),
		Command:      command,
		CookID:       request.GetCookId(),
	})
	if err != nil {
		return nil, err
	}
	return &service.MutationSetCookOnLineResponse{SetCookOnLine: payload}, nil
}

// MutationRetireCook takes a cook off line out of the Cook Pool.
func (s *BffService) MutationRetireCook(ctx context.Context, request *service.MutationRetireCookRequest) (*service.MutationRetireCookResponse, error) {
	payload, err := s.changeCookPool(ctx, "retireCook", applications.CookPoolRequest{
		RestaurantID: request.GetRestaurantId(),
		Command:      applications.CookCommandRetire,
		CookID:       request.GetCookId(),
	})
	if err != nil {
		return nil, err
	}
	return &service.MutationRetireCookResponse{RetireCook: payload}, nil
}
