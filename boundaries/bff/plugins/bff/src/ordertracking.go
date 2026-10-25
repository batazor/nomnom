package main

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	service "github.com/batazor/nomnom/boundaries/bff/plugins/bff/generated"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/ordertracking/applications"
)

// orderTrackingSlice names the slice in "not available" errors.
const orderTrackingSlice = "order"

func (s *BffService) QueryOrder(ctx context.Context, request *service.QueryOrderRequest) (*service.QueryOrderResponse, error) {
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireSlice(orderTrackingSlice, s.tracking != nil); err != nil {
		return nil, err
	}
	view, err := s.tracking.getOrder.Handle(ctx, request.GetId())
	if errors.Is(err, applications.ErrOrderNotFound) {
		return &service.QueryOrderResponse{}, nil
	}
	if err != nil {
		return nil, internalError("order", err)
	}
	order, err := orderMessage(view)
	if err != nil {
		return nil, internalError("order", err)
	}
	return &service.QueryOrderResponse{Order: order}, nil
}

func (s *BffService) MutationRequestOrderCancellation(ctx context.Context, request *service.MutationRequestOrderCancellationRequest) (*service.MutationRequestOrderCancellationResponse, error) {
	input := request.GetInput()
	if input.GetOrderId() == "" || input.GetRequestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "orderId and requestId are required")
	}
	ctx, err := s.requireCustomer(ctx)
	if err != nil {
		return nil, err
	}
	if err := requireSlice(orderTrackingSlice, s.tracking != nil); err != nil {
		return nil, err
	}
	result, err := s.tracking.requestCancellation.Handle(ctx, applications.CancellationRequest{
		OrderID:   input.GetOrderId(),
		RequestID: input.GetRequestId(),
		Reason:    input.GetReason().GetValue(),
	})
	if errors.Is(err, applications.ErrOrderNotFound) {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	if err != nil {
		return nil, internalError("requestOrderCancellation", err)
	}
	payload, err := cancellationPayload(result)
	if err != nil {
		return nil, internalError("requestOrderCancellation", err)
	}
	return &service.MutationRequestOrderCancellationResponse{RequestOrderCancellation: payload}, nil
}
